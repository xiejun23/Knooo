package studio

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
	"github.com/xiejun23/Knooo/internal/update"
	_ "modernc.org/sqlite"
)

type app struct {
	mu             sync.RWMutex
	refresh        sync.Mutex
	scanned        bool // Protected by refresh.
	reader         *sessions.Store
	sessions       []sessions.Session
	warnings       []string
	coverage       sessions.ScanReport
	sources        map[string][]string
	db             *sql.DB
	demo           bool
	keys           connectionKeys
	data           string
	discoveryNonce string
	review         reviewWorker
	revisions      map[string]string
}

type note struct {
	ID        int64         `json:"id"`
	SessionID string        `json:"sessionId"`
	Topic     distill.Topic `json:"topic"`
}

type overviewItem struct {
	sessions.Session
	Count    int  `json:"count"`
	Archived bool `json:"archived,omitempty"`
}

func New(reader *sessions.Store, data string, demo bool) (*app, error) {
	path := ":memory:"
	if !demo && data != "" {
		if err := os.MkdirAll(data, 0700); err != nil {
			return nil, err
		}
		path = filepath.Join(data, "studio.sqlite")
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS topics (session TEXT PRIMARY KEY, content TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS notes (id INTEGER PRIMARY KEY, session TEXT NOT NULL, content TEXT NOT NULL, UNIQUE(session, content));
 CREATE TABLE IF NOT EXISTS settings (name TEXT PRIMARY KEY, content TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS model_keys (id TEXT PRIMARY KEY, secret BLOB NOT NULL);
 CREATE TABLE IF NOT EXISTS archived_sessions (session TEXT PRIMARY KEY);
 CREATE TABLE IF NOT EXISTS reviews (session TEXT PRIMARY KEY, revision TEXT NOT NULL, content TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS review_failures (session TEXT PRIMARY KEY, revision TEXT NOT NULL, consumed INTEGER NOT NULL, message TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS report_documents (scope TEXT PRIMARY KEY, report TEXT NOT NULL, report_revision INTEGER NOT NULL DEFAULT 1, draft TEXT, draft_revision INTEGER NOT NULL DEFAULT 0);
 CREATE TABLE IF NOT EXISTS platform_logs (
   id INTEGER PRIMARY KEY, time TEXT NOT NULL, area TEXT NOT NULL, level TEXT NOT NULL,
   action TEXT NOT NULL, session TEXT NOT NULL, message TEXT NOT NULL, retry_at TEXT NOT NULL
 );
 CREATE INDEX IF NOT EXISTS idx_platform_logs_time ON platform_logs(time DESC, id DESC);
 CREATE INDEX IF NOT EXISTS idx_platform_logs_area_level ON platform_logs(area, level, id DESC);
 CREATE TRIGGER IF NOT EXISTS keep_recent_platform_logs AFTER INSERT ON platform_logs
 BEGIN
   DELETE FROM platform_logs WHERE id <= (SELECT id FROM platform_logs ORDER BY id DESC LIMIT 1 OFFSET 1000);
 END;`)
	if err != nil {
		db.Close()
		return nil, err
	}
	if !demo && data != "" && reader != nil {
		reader.SetCachePath(filepath.Join(data, "sessions-cache.v1.zst"))
	}
	a := &app{reader: reader, db: db, demo: demo, data: data, discoveryNonce: rand.Text(), sources: map[string][]string{}, keys: connectionKeys{values: map[string]string{}}}
	var config string
	if err := db.QueryRow(`SELECT content FROM settings WHERE name='sources'`).Scan(&config); err == nil {
		if err := json.Unmarshal([]byte(config), &a.sources); err != nil {
			db.Close()
			return nil, err
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		db.Close()
		return nil, err
	}
	if demo {
		a.sessions = demoSessions(time.Now())
		a.revisions = reviewRevisions(a.sessions)
		a.coverage = demoCoverage(a.sessions)
		if err := a.demoReviews(); err != nil {
			db.Close()
			return nil, err
		}
		if err := a.demoLogs(time.Now()); err != nil {
			db.Close()
			return nil, err
		}
	} else if id, err := a.reviewModel(); err != nil {
		db.Close()
		return nil, err
	} else if id != "" {
		a.startReview()
	}
	return a, nil
}

func (a *app) Close() error {
	a.review.stop()
	return a.db.Close()
}

func jsonReply(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(data)
}

func decode(w http.ResponseWriter, r *http.Request, value any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(value); err != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "请求格式无效", http.StatusBadRequest)
		return false
	}
	return true
}

func (a *app) Handler(addr, token string, ui http.Handler, version, changelog string, updates http.Handler) http.Handler {
	mux := http.NewServeMux()
	if updates != nil {
		mux.Handle("/api/update", updates)
	}
	mux.Handle("/", a.routes(ui, version, changelog))
	return localOnly(addr, token, mux)
}

// The per-launch capability also protects GET requests that contain private history.
func localOnly(addr, token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != addr || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+addr) {
			http.Error(w, "Local origin required", http.StatusForbidden)
			return
		}
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'; form-action 'self'")
		w.Header().Set("Cache-Control", "no-store")
		if strings.HasPrefix(r.URL.Path, "/api/") && subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+token)) != 1 {
			http.Error(w, "请从 Knooo 启动程序打开工作台", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (a *app) routes(ui http.Handler, version, changelog string) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", ui)
	mux.HandleFunc("GET /api/about", func(w http.ResponseWriter, r *http.Request) {
		jsonReply(w, map[string]any{"version": version, "changelog": changelog, "repository": update.Repository, "demo": a.demo})
	})
	mux.HandleFunc("GET /api/sessions", func(w http.ResponseWriter, r *http.Request) {
		a.ensureSessionIndex()
		archived, err := a.archivedSessions(r.Context())
		if err != nil {
			http.Error(w, "无法读取会话归档", 500)
			return
		}
		a.mu.RLock()
		records, warnings, sources, coverage := a.sessions, a.warnings, a.sources, a.coverage
		a.mu.RUnlock()
		items := make([]overviewItem, 0, len(records))
		for _, item := range records {
			count := len(item.Messages)
			item.Messages = nil
			items = append(items, overviewItem{Session: item, Count: count, Archived: archived[item.ID]})
		}
		jsonReply(w, map[string]any{"sessions": items, "warnings": warnings, "sources": sources, "coverage": coverage})
	})
	mux.HandleFunc("POST /api/refresh", a.scan)
	mux.HandleFunc("GET /api/history/search", a.searchHistory)
	mux.HandleFunc("POST /api/history/archive", a.setHistoryArchive)
	mux.HandleFunc("GET /api/session", a.detail)
	mux.HandleFunc("POST /api/distill", a.generate)
	mux.HandleFunc("GET /api/connections", a.modelConnections)
	mux.HandleFunc("POST /api/connections", a.setModelConnection)
	mux.HandleFunc("GET /api/connections/discovery", a.discoveredModelConnections)
	mux.HandleFunc("POST /api/connections/discovery", a.importModelConnection)
	mux.HandleFunc("POST /api/models", a.providerModels)
	mux.HandleFunc("GET /api/search", a.search)
	mux.HandleFunc("GET /api/search/matches", a.searchMatches)
	mux.HandleFunc("GET /api/notes", a.notes)
	mux.HandleFunc("POST /api/notes", a.saveNote)
	mux.HandleFunc("GET /api/report", a.report)
	mux.HandleFunc("POST /api/report", a.report)
	mux.HandleFunc("GET /api/report/projects", a.reportProjects)
	mux.HandleFunc("GET /api/report/document", a.savedReport)
	mux.HandleFunc("POST /api/report/document", a.saveReportDraft)
	mux.HandleFunc("GET /api/reviews", a.reviews)
	mux.HandleFunc("POST /api/reviews/retry", a.retryReview)
	mux.HandleFunc("GET /api/logs", a.platformLogs)
	mux.HandleFunc("GET /api/review-settings", a.getReviewModel)
	mux.HandleFunc("POST /api/review-settings", a.setReviewModel)
	mux.HandleFunc("GET /api/desktop-startup", a.desktopStartup)
	mux.HandleFunc("POST /api/desktop-startup", a.desktopStartup)
	return mux
}

func (a *app) ensureSessionIndex() {
	if a.demo || a.reader == nil {
		return
	}
	a.refresh.Lock()
	defer a.refresh.Unlock()
	if !a.scanned {
		a.scanSources(a.sources, "startup")
	}
}

func (a *app) scan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Sources *map[string][]string `json:"sources"`
		Manual  bool                 `json:"manual"`
	}
	if !decode(w, r, &body) {
		return
	}
	a.refresh.Lock()
	defer a.refresh.Unlock()
	if a.demo {
		jsonReply(w, map[string]bool{"ok": true})
		return
	}
	a.mu.RLock()
	sources := a.sources
	a.mu.RUnlock()
	trigger := "auto"
	if body.Manual {
		trigger = "manual"
	}
	if body.Sources != nil {
		trigger = "sources"
		for agent, dirs := range *body.Sources {
			if !sessions.SupportedAgent(agent) || len(dirs) > 12 {
				http.Error(w, "可为每个 Agent 配置最多 12 个目录", 400)
				return
			}
			for _, dir := range dirs {
				if !filepath.IsAbs(dir) {
					http.Error(w, "请输入绝对目录路径", 400)
					return
				}
			}
		}
		sources = *body.Sources
		content, _ := json.Marshal(sources)
		if _, err := a.db.Exec(`INSERT INTO settings VALUES('sources',?) ON CONFLICT(name) DO UPDATE SET content=excluded.content`, string(content)); err != nil {
			http.Error(w, "无法保存来源设置", 500)
			return
		}
	}
	a.scanSources(sources, trigger)
	a.wakeReview(false)
	jsonReply(w, map[string]bool{"ok": true})
}

func (a *app) scanSources(sources map[string][]string, trigger string) {
	started := time.Now()
	records, warnings, coverage := a.reader.Scan(sources)
	slices.SortStableFunc(records, func(x, y sessions.Session) int { return compareTime(y.Updated, x.Updated) })
	revisions := coverage.MessageRevisions
	a.mu.Lock()
	previousRevisions := a.revisions
	changed := !maps.Equal(a.revisions, revisions) || !slices.Equal(a.warnings, warnings) ||
		!slices.Equal(a.coverage.Roots, coverage.Roots) || !slices.Equal(a.coverage.Environments, coverage.Environments)
	a.sessions, a.warnings, a.sources, a.coverage = records, warnings, sources, coverage
	a.revisions = revisions
	a.mu.Unlock()
	elapsed := time.Since(started)

	wasScanned := a.scanned
	a.scanned = true

	if !wasScanned {
		trigger = "startup"
	}
	if !wasScanned || changed || trigger == "manual" || trigger == "sources" {
		added, updated, removed := 0, 0, 0
		if wasScanned {
			for id, rev := range revisions {
				prev, ok := previousRevisions[id]
				if !ok {
					added++
				} else if prev != rev {
					updated++
				}
			}
			for id := range previousRevisions {
				if _, ok := revisions[id]; !ok {
					removed++
				}
			}
		}
		customDirs := 0
		for _, dirs := range sources {
			customDirs += len(dirs)
		}
		level := "info"
		if len(warnings) > 0 {
			level = "warning"
		}
		action, message := formatScanSummary(trigger, records, warnings, coverage, added, updated, removed, customDirs, elapsed)
		a.recordEvent(platformEvent{Area: "scan", Level: level, Action: action, Message: message})
	}
}

func compareTime(x, y string) int {
	first, _ := time.Parse(time.RFC3339Nano, x)
	second, _ := time.Parse(time.RFC3339Nano, y)
	return first.Compare(second)
}

func (a *app) session(id string) (sessions.Session, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, item := range a.sessions {
		if item.ID == id {
			return item, true
		}
	}
	return sessions.Session{}, false
}

func inputs(s sessions.Session) []distill.Input {
	result := make([]distill.Input, 0, len(s.Messages))
	for _, m := range s.Messages {
		result = append(result, distill.Input{ID: m.ID, Role: m.Role, Text: m.Text})
	}
	return result
}

func (a *app) allTopics(ctx context.Context) (map[string][]distill.Topic, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT session,content FROM topics`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string][]distill.Topic{}
	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return nil, err
		}
		var topics []distill.Topic
		if err := json.Unmarshal([]byte(content), &topics); err != nil {
			continue
		}
		result[id] = topics
	}
	return result, rows.Err()
}

func (a *app) detail(w http.ResponseWriter, r *http.Request) {
	s, ok := a.session(r.URL.Query().Get("id"))
	if !ok {
		http.Error(w, "会话暂不可用，请刷新来源", 404)
		return
	}
	var list []distill.Topic
	var content string
	err := a.db.QueryRowContext(r.Context(), `SELECT content FROM topics WHERE session=?`, s.ID).Scan(&content)
	switch {
	case err == nil:
		err = json.Unmarshal([]byte(content), &list)
	case errors.Is(err, sql.ErrNoRows):
		err = nil
	}
	if err != nil {
		http.Error(w, "无法读取提炼结果", 500)
		return
	}
	generated := len(list) > 0
	switch {
	case generated:
		list = checkTopicEvidence(list, s.Messages)
	default:
		review, err := a.sessionReview(r.Context(), s)
		if err != nil {
			http.Error(w, "无法读取工作回顾", 500)
			return
		}
		if review != nil {
			list, generated = review, true
		} else {
			list = stampTopicEvidence(distill.Local(s.Title, inputs(s)), s.Messages)
		}
	}
	response := map[string]any{"session": s, "topics": list, "generated": generated}
	if version := r.URL.Query().Get("evidenceVersion"); version != "" {
		id := r.URL.Query().Get("anchor")
		index := slices.IndexFunc(s.Messages, func(m sessions.Message) bool { return m.ID == id })
		response["staleAnchor"] = index < 0 || messageEvidenceVersion(s.Messages[index]) != version
	}
	jsonReply(w, response)
}

func (a *app) generate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID           string `json:"id"`
		ConnectionID string `json:"connectionId"`
		Model        string `json:"model"`
		Preview      bool   `json:"preview"`
		Confirmation string `json:"confirmation"`
	}
	if !decode(w, r, &body) {
		return
	}
	s, ok := a.session(body.ID)
	if !ok {
		http.Error(w, "找不到会话", 404)
		return
	}
	messages := inputs(s)
	var list []distill.Topic
	if body.ConnectionID != "" {
		if a.demo {
			http.Error(w, "演示模式只运行本地提取", 400)
			return
		}
		config, err := a.connectionConfig(body.ConnectionID)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if model := strings.TrimSpace(body.Model); model != "" {
			config.Model = model
		}
		if config.Model == "" || len(config.Model) > 240 {
			http.Error(w, "请选择或填写有效的模型 ID", 400)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		plan, err := distill.Prepare(config, selectedModel(ctx, config), messages)
		if err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
		if body.Preview {
			jsonReply(w, plan)
			return
		}
		if body.Confirmation != plan.Confirmation {
			http.Error(w, "待发送内容或预算已变化，请重新预览并确认", 409)
			return
		}
		list, err = plan.Generate(ctx, config)
		if err != nil {
			http.Error(w, err.Error(), 422)
			return
		}
	} else if body.Preview {
		http.Error(w, "请选择模型连接后预览提炼范围", 400)
		return
	} else {
		list = distill.Local(s.Title, messages)
	}
	list = stampTopicEvidence(list, s.Messages)
	content, _ := json.Marshal(list)
	if _, err := a.db.Exec(`INSERT INTO topics VALUES(?,?) ON CONFLICT(session) DO UPDATE SET content=excluded.content`, s.ID, string(content)); err != nil {
		http.Error(w, "无法保存提炼结果", 500)
		return
	}
	jsonReply(w, list)
}

func (a *app) notes(w http.ResponseWriter, r *http.Request) {
	a.ensureSessionIndex()
	snapshot := a.sessionsSnapshot()
	messages := make(map[string][]sessions.Message, len(snapshot))
	for _, s := range snapshot {
		messages[s.ID] = s.Messages
	}
	rows, err := a.db.QueryContext(r.Context(), `SELECT id,session,content FROM notes ORDER BY id DESC`)
	if err != nil {
		http.Error(w, "无法读取知识库", 500)
		return
	}
	defer rows.Close()
	items := []note{}
	for rows.Next() {
		var n note
		var content string
		if rows.Scan(&n.ID, &n.SessionID, &content) != nil || json.Unmarshal([]byte(content), &n.Topic) != nil {
			http.Error(w, "无法读取知识条目", 500)
			return
		}
		n.Topic = checkTopicEvidence([]distill.Topic{n.Topic}, messages[n.SessionID])[0]
		items = append(items, n)
	}
	if rows.Err() != nil {
		http.Error(w, "无法读取知识库", 500)
		return
	}
	jsonReply(w, items)
}

func (a *app) saveNote(w http.ResponseWriter, r *http.Request) {
	var n note
	if !decode(w, r, &n) {
		return
	}
	s, ok := a.session(n.SessionID)
	if !ok || strings.TrimSpace(n.Topic.Title) == "" || len(n.Topic.Evidence) == 0 {
		http.Error(w, "知识条目需要标题和原文依据", 400)
		return
	}
	current := stampTopicEvidence([]distill.Topic{n.Topic}, s.Messages)[0]
	if n.Topic.EvidenceStale || !sameTopicEvidence(n.Topic, current) {
		http.Error(w, "原文依据已失效，请重新打开会话并确认依据", 409)
		return
	}
	n.Topic = current
	content, _ := json.Marshal(n.Topic)
	if _, err := a.db.Exec(`INSERT INTO notes(session, content) VALUES(?,?) ON CONFLICT(session, content) DO NOTHING`, n.SessionID, string(content)); err != nil {
		http.Error(w, "无法保存知识条目", 500)
		return
	}
	jsonReply(w, map[string]bool{"ok": true})
}

func (a *app) sessionsSnapshot() []sessions.Session {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions
}
