package studio

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

const reviewInterval = time.Minute
const reviewIdleDelay = 2 * time.Hour
const reviewConcurrency = 3
const reviewResponseAttempts = 3
const reviewWindowToday = 1

var errReviewChanged = errors.New("review checkpoint changed")
var errReviewWindowInvalid = errors.New("自动回顾范围无效，请在本机设置中重新保存")

type reviewWorker struct {
	sync.Mutex
	wake          chan struct{}
	done          chan struct{}
	close         context.CancelFunc
	cancelRun     context.CancelFunc
	running       map[string]string
	yieldToManual bool
	message       string
	settingsError bool
	rejected      map[string]reviewIssue
}

type reviewIssue struct {
	revision string
	message  string
	consumed int
	attempts int
	retryAt  time.Time
}

func (issue reviewIssue) blocks(revision string, now time.Time) bool {
	return issue.revision == revision && (issue.retryAt.IsZero() || now.Before(issue.retryAt))
}

func deferTransientReview(previous reviewIssue, revision string, err error, now time.Time) reviewIssue {
	attempts := 1
	if previous.revision == revision && previous.attempts > 0 {
		attempts = previous.attempts + 1
	}
	backoff := 5 * time.Minute
	switch {
	case attempts == 2:
		backoff = 15 * time.Minute
	case attempts >= 3:
		backoff = 30 * time.Minute
	}
	return reviewIssue{
		revision: revision,
		message:  err.Error(),
		attempts: attempts,
		retryAt:  now.Add(backoff),
	}
}

func rejectReview(previous reviewIssue, revision string, err error, now time.Time) reviewIssue {
	issue := reviewIssue{revision: revision, message: err.Error()}
	var invalid *distill.InvalidReviewResponse
	if !errors.As(err, &invalid) || invalid.Consumed == 0 {
		return issue
	}
	issue.consumed, issue.attempts = invalid.Consumed, 1
	if previous.revision == revision && previous.consumed == issue.consumed {
		issue.attempts += previous.attempts
	}
	if issue.attempts < reviewResponseAttempts {
		issue.retryAt = now.Add(time.Duration(issue.attempts) * reviewInterval)
	}
	return issue
}

type storedReview struct {
	revision string
	raw      string
	reviewCheckpoint
}

type reviewCheckpoint struct {
	Consumed int             `json:"consumed"`
	Topics   []distill.Topic `json:"topics"`
	Retry    *reviewRetry    `json:"retry,omitempty"`
}

type reviewRetry struct {
	Token    string          `json:"token"`
	Revision string          `json:"revision,omitempty"`
	Consumed int             `json:"consumed"`
	Topics   []distill.Topic `json:"topics"`
}

func (review *storedReview) decode(raw string) error {
	review.raw = raw
	if strings.HasPrefix(strings.TrimSpace(raw), "{") {
		return json.Unmarshal([]byte(raw), &review.reviewCheckpoint)
	}
	review.Consumed = -1 // Legacy arrays have a full revision but no message count.
	return json.Unmarshal([]byte(raw), &review.Topics)
}

func (review storedReview) verified(messages []sessions.Message, revision string) (reviewCheckpoint, bool) {
	checkpoint := review.reviewCheckpoint
	if checkpoint.Consumed == -1 && review.revision == revision {
		checkpoint.Consumed = len(messages)
	}
	if review.revision == "" || checkpoint.Consumed < 0 || checkpoint.Consumed > len(messages) {
		return reviewCheckpoint{}, false
	}
	if checkpoint.Consumed < len(messages) {
		revision = reviewRevision(messages[:checkpoint.Consumed])
	}
	if review.revision != revision {
		return reviewCheckpoint{}, false
	}
	return checkpoint, true
}

type reviewEntry struct {
	Session      overviewItem    `json:"session"`
	State        string          `json:"state"`
	Topics       []distill.Topic `json:"topics,omitempty"`
	AutoReviewAt string          `json:"autoReviewAt,omitempty"`
}

func reviewRevisions(items []sessions.Session) map[string]string {
	revisions := make(map[string]string, len(items))
	for _, item := range items {
		revisions[item.ID] = reviewRevision(item.Messages)
	}
	return revisions
}

func reviewRevision(messages []sessions.Message) string {
	return sessions.MessageRevision(messages)
}

func (a *app) reviewSnapshot() ([]sessions.Session, map[string]string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions, a.revisions
}

func (a *app) reviewModel() (string, error) {
	var id string
	err := a.db.QueryRow(`SELECT content FROM settings WHERE name='review_model'`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

const reviewSettingsQuery = `SELECT
	COALESCE((SELECT content FROM settings WHERE name='review_model'), ''),
	COALESCE((SELECT content FROM settings WHERE name='review_window_days'), '0')`

func validReviewWindowDays(days int) bool {
	switch days {
	case 0, reviewWindowToday, 3, 7, 30, 90:
		return true
	default:
		return false
	}
}

func parseReviewWindowDays(raw string) (int, error) {
	days, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || !validReviewWindowDays(days) {
		return 0, errReviewWindowInvalid
	}
	return days, nil
}

func (a *app) reviewSettings(ctx context.Context) (string, int, error) {
	var id, raw string
	if err := a.db.QueryRowContext(ctx, reviewSettingsQuery).Scan(&id, &raw); err != nil {
		return "", 0, err
	}
	days, err := parseReviewWindowDays(raw)
	return id, days, err
}

func (a *app) getReviewModel(w http.ResponseWriter, r *http.Request) {
	id, days, err := a.reviewSettings(r.Context())
	if errors.Is(err, errReviewWindowInvalid) {
		jsonReply(w, map[string]any{"connectionId": id, "settingsError": err.Error()})
		return
	}
	if err != nil {
		http.Error(w, "无法读取自动工作回顾设置", http.StatusInternalServerError)
		return
	}
	jsonReply(w, map[string]any{"connectionId": id, "maxAgeDays": days})
}

func (a *app) setReviewModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ConnectionID *string `json:"connectionId"`
		MaxAgeDays   *int    `json:"maxAgeDays"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.ConnectionID == nil || a.demo {
		http.Error(w, "无法保存自动工作回顾设置", http.StatusBadRequest)
		return
	}
	id := *body.ConnectionID
	if id != "" {
		config, err := a.connectionConfig(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if config.Model == "" {
			http.Error(w, "请先为连接保存默认模型 ID", http.StatusBadRequest)
			return
		}
	}
	days := 0
	if body.MaxAgeDays != nil {
		days = *body.MaxAgeDays
		if !validReviewWindowDays(days) {
			http.Error(w, "自动提炼范围无效", http.StatusBadRequest)
			return
		}
	}
	ctx := r.Context()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		http.Error(w, "无法保存自动工作回顾设置", http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()
	var previousID, rawDays string
	if err := tx.QueryRowContext(ctx, reviewSettingsQuery).Scan(&previousID, &rawDays); err != nil {
		http.Error(w, "无法读取自动工作回顾设置", http.StatusInternalServerError)
		return
	}
	if body.MaxAgeDays == nil {
		days, err = parseReviewWindowDays(rawDays)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO settings VALUES('review_model',?),('review_window_days',?)
		ON CONFLICT(name) DO UPDATE SET content=excluded.content`, id, strconv.Itoa(days)); err != nil {
		http.Error(w, "无法保存自动工作回顾设置", http.StatusInternalServerError)
		return
	}
	if id != "" && id != previousID {
		if _, err := tx.ExecContext(ctx, `DELETE FROM review_failures`); err != nil {
			http.Error(w, "无法保存自动工作回顾设置", http.StatusInternalServerError)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "无法保存自动工作回顾设置", http.StatusInternalServerError)
		return
	}
	a.wakeReview(true)
	if id != previousID {
		a.keys.Lock()
		prevProfile, prevFound := a.loadSavedModelConnection(previousID)
		nextProfile, nextFound := a.loadSavedModelConnection(id)
		a.keys.Unlock()
		formatTarget := func(p modelConnection, found bool, rawID string) string {
			if rawID == "" {
				return "未配置"
			}
			if !found {
				return rawID
			}
			if p.Model != "" {
				return fmt.Sprintf("%s / %s", p.Name, p.Model)
			}
			return p.Name
		}
		action := "更新自动回顾默认模型"
		if id == "" {
			action = "关闭自动回顾默认模型"
		}
		a.recordEvent(platformEvent{
			Area:    "review",
			Level:   "info",
			Action:  action,
			Message: fmt.Sprintf("当前设置：%s（原：%s）", formatTarget(nextProfile, nextFound, id), formatTarget(prevProfile, prevFound, previousID)),
		})
	}
	if id != "" {
		a.startReview()
	}
	jsonReply(w, map[string]any{"connectionId": id, "maxAgeDays": days})
}

func (a *app) retryReview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID          string `json:"id"`
		Incremental bool   `json:"incremental"`
	}
	if !decode(w, r, &body) {
		return
	}
	if a.demo || strings.TrimSpace(body.ID) == "" {
		http.Error(w, "无法重新提炼该会话", http.StatusBadRequest)
		return
	}
	id, _, err := a.reviewSettings(r.Context())
	if err != nil {
		http.Error(w, "无法读取自动工作回顾设置", http.StatusInternalServerError)
		return
	}
	if id == "" {
		http.Error(w, "请先在「本机设置」中选择自动工作回顾的默认算力提供商", http.StatusBadRequest)
		return
	}
	a.ensureSessionIndex()
	session, found := a.session(body.ID)
	if !found {
		http.Error(w, "会话不存在", http.StatusNotFound)
		return
	}
	if len(session.Messages) == 0 {
		http.Error(w, "会话没有可重新提炼的消息", http.StatusBadRequest)
		return
	}
	a.review.Lock()
	queued, err := a.queueReviewRetry(r.Context(), session, body.Incremental)
	if err != nil {
		a.review.Unlock()
		http.Error(w, "无法将会话加入提炼队列", http.StatusInternalServerError)
		return
	}
	if queued {
		if body.Incremental {
			a.review.yieldToManual = true
		} else if a.review.cancelRun != nil {
			a.review.cancelRun()
		}
		delete(a.review.rejected, body.ID)
		a.review.message = ""
	}
	a.review.Unlock()
	if queued {
		a.startReview()
		a.wakeReview(false)
	}
	jsonReply(w, map[string]bool{"ok": true, "queued": queued})
}

// The caller holds the worker lock so the session cannot start while it is queued.
func (a *app) queueReviewRetry(ctx context.Context, session sessions.Session, incremental bool) (bool, error) {
	id := session.ID
	if incremental && a.review.running[id] != "" {
		return false, nil
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var previous storedReview
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT revision,content FROM reviews WHERE session=?`, id).Scan(&previous.revision, &raw)
	if err == nil {
		if err := previous.decode(raw); err != nil {
			return false, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	pending := incremental && previous.Retry != nil
	if !pending {
		replacement := &reviewRetry{Token: rand.Text()}
		if incremental {
			progress, valid := previous.verified(session.Messages, reviewRevision(session.Messages))
			if valid {
				if progress.Consumed == len(session.Messages) {
					return false, nil
				}
				replacement.Revision, replacement.Consumed, replacement.Topics = previous.revision, progress.Consumed, progress.Topics
			}
		}
		checkpoint := previous.reviewCheckpoint
		checkpoint.Retry = replacement
		revision := previous.revision
		if revision == "" {
			revision = reviewRevision(nil)
		}
		if _, err := writeReview(ctx, tx, id, revision, checkpoint, previous); err != nil {
			return false, err
		}
	}
	deleted, err := tx.ExecContext(ctx, `DELETE FROM review_failures WHERE session=?`, id)
	if err != nil {
		return false, err
	}
	if pending && a.review.rejected[id].revision == "" {
		count, err := deleted.RowsAffected()
		if err != nil || count == 0 {
			return false, err
		}
	}
	return true, tx.Commit()
}

func (a *app) startReview() {
	worker := &a.review
	worker.Lock()
	defer worker.Unlock()
	if worker.wake == nil {
		ctx, cancel := context.WithCancel(context.Background())
		worker.wake, worker.done, worker.close = make(chan struct{}, 1), make(chan struct{}), cancel
		worker.wake <- struct{}{}
		go a.runReviews(ctx, worker.wake, worker.done)
	}
}

func (a *app) wakeReview(interrupt bool) {
	worker := &a.review
	worker.Lock()
	defer worker.Unlock()
	if worker.wake == nil {
		return
	}
	if interrupt {
		if worker.cancelRun != nil {
			worker.cancelRun()
		}
		worker.message, worker.settingsError, worker.rejected = "", false, nil
	}
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *reviewWorker) stop() {
	worker.Lock()
	cancel, done := worker.close, worker.done
	worker.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (a *app) runReviews(ctx context.Context, wake <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(reviewInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		scan := false
		select {
		case <-ctx.Done():
			return
		case <-wake:
		case <-ticker.C:
			scan = true
		}
		run, cancel := context.WithCancel(ctx)
		a.review.Lock()
		a.review.cancelRun = cancel
		a.review.Unlock()
		a.processReviews(run, scan)
		cancel()
		a.review.Lock()
		a.review.cancelRun, a.review.running = nil, nil
		a.review.Unlock()
	}
}

func (a *app) processReviews(ctx context.Context, scan bool) {
	a.review.Lock()
	// Requests arriving after this point can be missing from this run's snapshot.
	a.review.yieldToManual = false
	a.review.Unlock()
	id, maxAgeDays, err := a.reviewSettings(ctx)
	if err != nil {
		a.reviewFailure(ctx, "无法读取自动工作回顾设置，请在本机设置中重新保存", true)
		return
	}
	if id == "" || ctx.Err() != nil {
		return
	}
	config, err := a.connectionConfig(id)
	if err != nil {
		a.reviewFailure(ctx, err.Error(), true)
		return
	}
	if config.Model == "" {
		a.reviewFailure(ctx, "请先为连接保存默认模型 ID", true)
		return
	}
	if scan && a.reader != nil {
		a.refresh.Lock()
		a.scanSources(a.sources, "auto")
		a.refresh.Unlock()
	} else {
		a.ensureSessionIndex()
	}
	cached, err := a.readReviews(ctx)
	if err != nil {
		a.reviewFailure(ctx, "无法读取工作回顾进度", false)
		return
	}
	archived, err := a.archivedSessions(ctx)
	if err != nil {
		a.reviewFailure(ctx, "无法读取会话归档", false)
		return
	}
	persistedFailures, err := a.readReviewFailures(ctx)
	if err != nil {
		persistedFailures = nil
	}
	now := time.Now()
	items, revisions := a.reviewSnapshot()
	hasRetry := false
	for _, checkpoint := range cached {
		if checkpoint.Retry != nil {
			hasRetry = true
			break
		}
	}
	if hasRetry {
		ordered := make([]sessions.Session, 0, len(items))
		for _, item := range items {
			if cached[item.ID].Retry != nil {
				ordered = append(ordered, item)
			}
		}
		for _, item := range items {
			if cached[item.ID].Retry == nil {
				ordered = append(ordered, item)
			}
		}
		items = ordered
	}
	type reviewResult struct {
		id  string
		err error
	}
	results := make(chan reviewResult, reviewConcurrency)
	running := make(map[string]string, reviewConcurrency)
	a.review.Lock()
	a.review.running = running
	a.review.Unlock()

	var model *distill.Model
	consecutiveRejected := 0
	failed := false
	for next := 0; ; {
		for len(running) < reviewConcurrency && next < len(items) && ctx.Err() == nil {
			item := items[next]
			next++
			revision := revisions[item.ID]
			if archived[item.ID] {
				continue
			}
			previous := cached[item.ID]
			checkpoint, valid := previous.verified(item.Messages, revision)
			retry := previous.Retry
			if retry != nil {
				progress := storedReview{revision: retry.Revision, reviewCheckpoint: reviewCheckpoint{Consumed: retry.Consumed, Topics: retry.Topics}}
				checkpoint, _ = progress.verified(item.Messages, revision)
			} else if valid && checkpoint.Consumed == len(item.Messages) {
				if previous.Consumed == -1 {
					if _, err := a.saveReview(ctx, item.ID, revision, checkpoint, previous); err != nil && !errors.Is(err, errReviewChanged) {
						a.reviewFailure(ctx, "无法保存工作回顾进度", false)
						failed = true
						next = len(items)
					}
				}
				continue
			}
			if len(item.Messages) == 0 {
				continue
			}
			isManual := retry != nil
			if !isManual && (reviewOutsideWindow(item, maxAgeDays, now) || !reviewCooldownUntil(item, now).IsZero()) {
				continue
			}
			if failure, ok := persistedFailures[item.ID]; ok && failure.revision == revision {
				continue
			}
			a.review.Lock()
			if a.review.yieldToManual || ctx.Err() != nil {
				a.review.Unlock()
				next = len(items)
				break
			}
			if a.review.rejected[item.ID].blocks(revision, time.Now()) {
				a.review.Unlock()
				continue
			}
			running[item.ID] = revision
			a.review.Unlock()
			if model == nil {
				selected := selectedModel(ctx, config)
				model = &selected
			}
			go func(model distill.Model) {
				err := distill.Review(ctx, config, model, inputs(item), checkpoint.Consumed, checkpoint.Topics, func(consumed int, topics []distill.Topic) error {
					prefix := revision
					if consumed < len(item.Messages) {
						prefix = reviewRevision(item.Messages[:consumed])
					}
					next := reviewCheckpoint{Consumed: consumed, Topics: topics}
					if retry != nil && consumed < len(item.Messages) {
						// Keep the existing review visible until the full replacement succeeds.
						staged := previous.reviewCheckpoint
						staged.Retry = &reviewRetry{Token: retry.Token, Revision: prefix, Consumed: consumed, Topics: topics}
						prefix, next = previous.revision, staged
					}
					var err error
					previous, err = a.saveReview(ctx, item.ID, prefix, next, previous)
					if err != nil && !errors.Is(err, errReviewChanged) {
						return errors.New("无法保存工作回顾进度")
					}
					return err
				})
				results <- reviewResult{id: item.ID, err: err}
			}(*model)
		}
		if len(running) == 0 {
			return
		}
		// Drain admitted sessions before another run can use the connection.
		result := <-results
		revision := running[result.id]
		err := result.err
		event := platformEvent{Area: "review", SessionID: result.id}
		a.review.Lock()
		if ctx.Err() == nil {
			switch {
			case errors.Is(err, errReviewChanged):
			case errors.Is(err, distill.ErrReviewResponse):
				if a.review.rejected == nil {
					a.review.rejected = make(map[string]reviewIssue)
				}
				issue := rejectReview(a.review.rejected[result.id], revision, err, time.Now())
				a.review.rejected[result.id] = issue
				if issue.retryAt.IsZero() {
					a.saveReviewFailure(ctx, result.id, issue)
				}
				event.Level, event.Action, event.Message = "error", "回顾内容校验失败", issue.message
				if issue.attempts > 0 {
					event.Message = fmt.Sprintf("%s（已处理 %d 条消息，当前批次第 %d 次失败）", event.Message, issue.consumed, issue.attempts)
				}
				if !issue.retryAt.IsZero() {
					event.RetryAt = issue.retryAt.UTC().Format(time.RFC3339Nano)
				}
				consecutiveRejected++
				if consecutiveRejected == 3 {
					next = len(items)
				}
			case err != nil:
				if a.review.rejected == nil {
					a.review.rejected = make(map[string]reviewIssue)
				}
				issue := deferTransientReview(a.review.rejected[result.id], revision, err, time.Now())
				a.review.rejected[result.id] = issue
				event.Level, event.Action, event.Message = "error", "自动回顾失败", err.Error()
				event.RetryAt = issue.retryAt.UTC().Format(time.RFC3339Nano)
				if !failed {
					a.review.message, a.review.settingsError = err.Error(), false
					failed = true
					next = len(items)
				}
			default:
				event.Level, event.Action = "info", "完成自动回顾"
				consecutiveRejected = 0
				delete(a.review.rejected, result.id)
				a.clearReviewFailure(ctx, result.id)
				if !failed {
					a.review.message, a.review.settingsError = "", false
				}
			}
		}
		delete(running, result.id)
		a.review.Unlock()
		if event.Level != "" {
			if event.Level == "error" {
				identity := formatModelIdentity(modelConnection{}, config)
				if origin := safeLogURL(config.BaseURL); origin != "" {
					identity += " · 地址：" + origin
				}
				event.Message += "\n" + identity
			}
			a.recordEvent(event)
		}
	}
}

func (a *app) reviewFailure(ctx context.Context, message string, settings bool) {
	a.review.Lock()
	changed := ctx.Err() == nil && (a.review.message != message || a.review.settingsError != settings)
	if changed {
		a.review.message, a.review.settingsError = message, settings
	}
	a.review.Unlock()
	if changed {
		a.recordEvent(platformEvent{Area: "review", Level: "error", Action: "自动回顾暂停", Message: message})
	}
}

func (a *app) saveReview(ctx context.Context, id, revision string, checkpoint reviewCheckpoint, previous storedReview) (storedReview, error) {
	return writeReview(ctx, a.db, id, revision, checkpoint, previous)
}

func writeReview(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, id, revision string, checkpoint reviewCheckpoint, previous storedReview) (storedReview, error) {
	raw, err := json.Marshal(checkpoint)
	if err != nil {
		return storedReview{}, err
	}
	var result sql.Result
	if previous.revision == "" {
		result, err = db.ExecContext(ctx, `INSERT INTO reviews(session,revision,content) VALUES(?,?,?) ON CONFLICT(session) DO NOTHING`, id, revision, string(raw))
	} else {
		result, err = db.ExecContext(ctx, `UPDATE reviews SET revision=?,content=? WHERE session=? AND revision=? AND content=?`,
			revision, string(raw), id, previous.revision, previous.raw)
	}
	if err != nil {
		return storedReview{}, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return storedReview{}, err
	}
	if changed == 0 {
		return storedReview{}, errReviewChanged
	}
	return storedReview{revision: revision, raw: string(raw), reviewCheckpoint: checkpoint}, nil
}

func (a *app) readReviews(ctx context.Context) (map[string]storedReview, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT session,revision,content FROM reviews`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]storedReview{}
	for rows.Next() {
		var id, raw string
		var item storedReview
		if err := rows.Scan(&id, &item.revision, &raw); err != nil {
			return nil, err
		}
		if err := item.decode(raw); err != nil {
			return nil, err
		}
		result[id] = item
	}
	return result, rows.Err()
}

func (a *app) sessionReview(ctx context.Context, s sessions.Session) ([]distill.Topic, error) {
	var raw string
	var review storedReview
	err := a.db.QueryRowContext(ctx, `SELECT revision,content FROM reviews WHERE session=?`, s.ID).Scan(&review.revision, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := review.decode(raw); err != nil {
		return nil, err
	}
	checkpoint, valid := review.verified(s.Messages, reviewRevision(s.Messages))
	if !valid || checkpoint.Consumed < len(s.Messages) && len(checkpoint.Topics) == 0 {
		return nil, nil
	}
	return stampTopicEvidence(publicReviewTopics(checkpoint.Topics), s.Messages), nil
}

func publicReviewTopics(topics []distill.Topic) []distill.Topic {
	topics = distill.ConsolidateReviewTopics(topics)
	for _, topic := range topics {
		if len(topic.ReviewHistory) == 0 {
			continue
		}
		result := append([]distill.Topic(nil), topics...)
		for i := range result {
			result[i].ReviewHistory = nil
		}
		return result
	}
	return topics
}

func reviewWindow(query url.Values) (time.Time, time.Time, error) {
	from, fromErr := time.Parse(time.RFC3339, query.Get("from"))
	to, toErr := time.Parse(time.RFC3339, query.Get("to"))
	if fromErr != nil || toErr != nil || !from.Before(to) {
		return time.Time{}, time.Time{}, errors.New("日期范围无效")
	}
	return from, to, nil
}

func inReviewScope(item sessions.Session, query url.Values) bool {
	return inProject(item, query.Get("project"), query.Get("location")) && item.MatchesAgent(query["agent"]...)
}

func inReviewWindow(value string, from, to time.Time) bool {
	timestamp, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && !timestamp.Before(from) && timestamp.Before(to)
}

func latestActivity(item sessions.Session) time.Time {
	var latest time.Time
	if ts, err := time.Parse(time.RFC3339Nano, item.Updated); err == nil {
		latest = ts
	}
	if n := len(item.Messages); n > 0 {
		if ts, err := time.Parse(time.RFC3339Nano, item.Messages[n-1].Timestamp); err == nil && ts.After(latest) {
			latest = ts
		}
	}
	return latest
}

func reviewCooldownUntil(item sessions.Session, now time.Time) time.Time {
	latest := latestActivity(item)
	if latest.IsZero() || latest.After(now.Add(time.Minute)) {
		return time.Time{}
	}
	if deadline := latest.Add(reviewIdleDelay); deadline.After(now) {
		return deadline
	}
	return time.Time{}
}

func (a *app) readReviewFailures(ctx context.Context) (map[string]reviewIssue, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT session,revision,consumed,message FROM review_failures`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]reviewIssue{}
	for rows.Next() {
		var id string
		var issue reviewIssue
		if err := rows.Scan(&id, &issue.revision, &issue.consumed, &issue.message); err != nil {
			return nil, err
		}
		out[id] = issue
	}
	return out, rows.Err()
}

func (a *app) saveReviewFailure(ctx context.Context, sessionID string, issue reviewIssue) {
	_, _ = a.db.ExecContext(ctx,
		`INSERT INTO review_failures(session,revision,consumed,message) VALUES(?,?,?,?) ON CONFLICT(session) DO UPDATE SET revision=excluded.revision,consumed=excluded.consumed,message=excluded.message`,
		sessionID, issue.revision, issue.consumed, issue.message)
}

func (a *app) clearReviewFailure(ctx context.Context, sessionID string) {
	_, _ = a.db.ExecContext(ctx, `DELETE FROM review_failures WHERE session=?`, sessionID)
}

func (a *app) readReviewsFor(ctx context.Context, ids []string) (map[string]storedReview, error) {
	result := make(map[string]storedReview, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	const chunk = 200
	for start := 0; start < len(ids); start += chunk {
		batch := ids[start:min(start+chunk, len(ids))]
		args := make([]any, len(batch))
		placeholders := make([]string, len(batch))
		for i, id := range batch {
			args[i] = id
			placeholders[i] = "?"
		}
		rows, err := a.db.QueryContext(ctx, `SELECT session,revision,content FROM reviews WHERE session IN (`+strings.Join(placeholders, ",")+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, raw string
			var item storedReview
			if err := rows.Scan(&id, &item.revision, &raw); err != nil {
				rows.Close()
				return nil, err
			}
			if err := item.decode(raw); err != nil {
				rows.Close()
				return nil, err
			}
			result[id] = item
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func reviewOutsideWindow(item sessions.Session, maxAgeDays int, now time.Time) bool {
	if maxAgeDays <= 0 {
		return false
	}
	latest := latestActivity(item)
	if latest.IsZero() {
		return maxAgeDays == reviewWindowToday
	}
	if maxAgeDays == reviewWindowToday {
		today := now.Format(time.DateOnly)
		activityDay := latest.In(now.Location()).Format(time.DateOnly)
		if activityDay == today {
			return false
		}
		return activityDay > today || latest.Add(reviewIdleDelay).In(now.Location()).Format(time.DateOnly) != today
	}
	return latest.Before(now.AddDate(0, 0, -maxAgeDays))
}

func (a *app) reviews(w http.ResponseWriter, r *http.Request) {
	id, maxAgeDays, settingsErr := a.reviewSettings(r.Context())
	result := struct {
		ConnectionID  string        `json:"connectionId"`
		Error         string        `json:"error,omitempty"`
		SettingsError bool          `json:"settingsError,omitempty"`
		Entries       []reviewEntry `json:"entries"`
	}{ConnectionID: id, Entries: []reviewEntry{}}
	query := r.URL.Query()
	from, to, err := reviewWindow(query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.ensureSessionIndex()
	archived, err := a.archivedSessions(r.Context())
	if err != nil {
		http.Error(w, "无法读取会话归档", http.StatusInternalServerError)
		return
	}
	items, revisions := a.reviewSnapshot()
	scoped := make([]sessions.Session, 0, 32)
	scopedIDs := make([]string, 0, 32)
	for _, item := range items {
		if archived[item.ID] || !inReviewScope(item, query) || !inReviewWindow(item.Updated, from, to) {
			continue
		}
		scoped = append(scoped, item)
		scopedIDs = append(scopedIDs, item.ID)
	}
	cached, err := a.readReviewsFor(r.Context(), scopedIDs)
	if err != nil {
		http.Error(w, "无法读取工作回顾", http.StatusInternalServerError)
		return
	}
	persistedFailures, _ := a.readReviewFailures(r.Context())
	now := time.Now()
	a.review.Lock()
	message := a.review.message
	result.SettingsError = a.review.settingsError
	running := maps.Clone(a.review.running)
	rejected := maps.Clone(a.review.rejected)
	a.review.Unlock()
	if settingsErr != nil {
		message = "无法读取自动工作回顾设置，请在本机设置中重新保存"
		result.SettingsError = true
	}
	result.Error = message
	for _, item := range scoped {
		revision := revisions[item.ID]
		previous := cached[item.ID]
		checkpoint, valid := previous.verified(item.Messages, revision)
		entry := reviewEntry{State: "queued", Topics: stampTopicEvidence(publicReviewTopics(checkpoint.Topics), item.Messages)}
		persisted := persistedFailures[item.ID]
		isManual := previous.Retry != nil
		cooldownUntil := reviewCooldownUntil(item, now)
		switch {
		case len(item.Messages) == 0 || !isManual && valid && checkpoint.Consumed == len(item.Messages):
			entry.State = "ready"
		case settingsErr != nil:
			entry.State = "error"
		case id == "":
			entry.State = "disabled"
		case running[item.ID] == revision:
			entry.State = "running"
		case message != "" || rejected[item.ID].revision == revision || persisted.revision == revision:
			entry.State = "error"
		case !isManual && reviewOutsideWindow(item, maxAgeDays, now):
			entry.State = "skipped"
		case !isManual && !cooldownUntil.IsZero():
			entry.State = "cooldown"
			entry.AutoReviewAt = cooldownUntil.UTC().Format(time.RFC3339Nano)
		}
		if entry.State == "error" && result.Error == "" {
			if issue := rejected[item.ID]; issue.revision == revision {
				result.Error = issue.message
			} else if persisted.revision == revision {
				result.Error = persisted.message
			}
		}
		entry.Session.Count = len(item.Messages)
		item.Messages = nil
		entry.Session.Session = item
		result.Entries = append(result.Entries, entry)
	}
	jsonReply(w, result)
}
