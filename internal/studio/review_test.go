package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestAutomaticReviewAndDateReport(t *testing.T) {
	const first = "2026-04-02T08:00:00+08:00"
	const second = "2026-04-03T08:00:00+08:00"
	item := sessions.Session{ID: "fixture", Agent: "antigravity", Project: "示例项目", Title: "回顾测试", Updated: second}
	item.Messages = append(item.Messages,
		sessions.Message{ID: "noise-env", Role: "user", Text: "<environment_context>ENV_MARKER</environment_context>", Timestamp: first},
		sessions.Message{ID: "noise-tool", Role: "tool", Text: "TOOL_MARKER", Timestamp: first},
		sessions.Message{ID: "noise-ack", Role: "user", Text: "谢谢。", Timestamp: first},
	)
	var expected []distill.Topic
	for index, subject := range []string{"索引", "路由", "缓存", "归档", "权限", "告警"} {
		userID, assistantID := fmt.Sprintf("question-%d", index), fmt.Sprintf("answer-%d", index)
		answerTime := first
		if index == 5 {
			answerTime = second
		}
		item.Messages = append(item.Messages,
			sessions.Message{ID: userID, Role: "user", Text: subject + "处有个异常，能否检查调用链并验证已记录的现象？", Timestamp: first},
			sessions.Message{ID: assistantID, Role: "assistant", Text: "已更改" + subject + "处理代码，场景验证成功，检查时保留了原始操作记录。", Timestamp: answerTime},
		)
		expected = append(expected, distill.Topic{
			Title: subject + "问题", Problem: "排查" + subject + "异常", Approach: "调整" + subject + "的处理逻辑",
			Outcome: subject + "验证通过", Status: "verified", Evidence: []string{userID, assistantID},
		})
	}
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"review-test","context_window":32768,"max_output_tokens":4096}]}`)
		case "/v1/chat/completions":
			calls.Add(1)
			var body struct {
				Model    string                     `json:"model"`
				Messages []struct{ Content string } `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "review-test" || len(body.Messages) != 2 {
				t.Errorf("unexpected review request: %+v (%v)", body, err)
				http.Error(w, "bad request", 400)
				return
			}
			var messages []distill.Input
			if err := json.Unmarshal([]byte(body.Messages[1].Content), &messages); err != nil || len(messages) != 12 || messages[0].ID != "question-0" || messages[11].ID != "answer-5" {
				t.Errorf("six turns must be sent in order without noise: %+v (%v)", messages, err)
			}
			content, _ := json.Marshal(map[string]any{"topics": expected})
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	a, err := New(nil, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions = []sessions.Session{item}
	a.revisions = reviewRevisions(a.sessions)
	handler := a.routes(http.NotFoundHandler(), "", "")
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != want {
			t.Fatalf("%s %s gave %d: %s", method, path, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	draft, _ := json.Marshal(map[string]string{"preset": "gateway", "name": "Test gateway", "model": "review-test", "baseUrl": gateway.URL + "/v1"})
	var profile modelConnection
	if err := json.Unmarshal(request("POST", "/api/connections", string(draft), 200), &profile); err != nil {
		t.Fatal(err)
	}
	window := url.Values{"kind": {"custom"}, "from": {"2026-04-02T00:00:00+08:00"}, "to": {"2026-04-04T00:00:00+08:00"}}
	reviewURL := "/api/reviews?" + window.Encode()
	if before := string(request("GET", reviewURL, "", 200)); !strings.Contains(before, `"state":"disabled"`) || strings.Contains(before, "能否检查") || calls.Load() != 0 {
		t.Fatalf("review should wait for opt-in without showing raw messages: %s", before)
	}
	request("POST", "/api/review-settings", fmt.Sprintf(`{"connectionId":%q}`, profile.ID), 200)
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw := request("GET", reviewURL, "", 200)
		var result struct{ Entries []reviewEntry }
		if err := json.Unmarshal(raw, &result); err != nil || len(result.Entries) != 1 {
			t.Fatalf("unexpected review queue: %s (%v)", raw, err)
		}
		entry := result.Entries[0]
		if entry.State == "ready" {
			if len(entry.Topics) != 6 || entry.Topics[5].Outcome != expected[5].Outcome || entry.Topics[5].Evidence[1] != "answer-5" || strings.Contains(string(raw), "能否检查") {
				t.Fatalf("expected six paraphrased questions with evidence: %s", raw)
			}
			break
		}
		if entry.State == "error" || time.Now().After(deadline) {
			t.Fatalf("background review did not complete: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	window.Set("to", "2026-04-03T00:00:00+08:00")
	firstDay := decodeTestReport(t, request("GET", "/api/report?"+window.Encode(), "", 200))
	if firstDay.Coverage != (reportCoverage{Ready: 1, Active: 1, Topics: 6}) || !strings.Contains(firstDay.Markdown, "2026-04-02 至 2026-04-02") ||
		!strings.Contains(firstDay.Markdown, "索引验证通过") || strings.Contains(firstDay.Markdown, "告警验证通过") || strings.Contains(firstDay.Markdown, "能否检查") ||
		!slices.Contains(reportSourceIDs(firstDay), "question-5") || slices.Contains(reportSourceIDs(firstDay), "answer-5") {
		t.Fatalf("first day should retain the question and exclude tomorrow's result: %+v", firstDay)
	}
	window.Set("from", "2026-04-03T00:00:00+08:00")
	request("GET", "/api/report?"+window.Encode(), "", 400)
	window.Set("to", "2026-04-04T00:00:00+08:00")
	secondDay := decodeTestReport(t, request("GET", "/api/report?"+window.Encode(), "", 200))
	if secondDay.Coverage != (reportCoverage{Ready: 1, Active: 1, Topics: 1}) || !strings.Contains(secondDay.Markdown, "告警验证通过") ||
		strings.Contains(secondDay.Markdown, "索引验证通过") || !slices.Equal(reportSourceIDs(secondDay), []string{"answer-5"}) {
		t.Fatalf("second day should include the verified work from that day: %+v", secondDay)
	}
	request("POST", "/api/review-settings", `{"connectionId":""}`, 200)
	if after := string(request("GET", reviewURL, "", 200)); !strings.Contains(after, `"state":"ready"`) || calls.Load() != 1 {
		t.Fatalf("saved review should be reusable without a provider request: %s", after)
	}
}

func TestAutomaticReviewProcessesSessionsConcurrently(t *testing.T) {
	const updated = "2026-04-03T08:00:00+08:00"
	started := make(chan chan struct{}, 4)
	unblock := make(chan struct{})
	var active, peak atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"review-test"}]}`)
		case "/v1/chat/completions":
			if _, err := io.Copy(io.Discard, r.Body); err != nil {
				t.Error(err)
				return
			}
			count := active.Add(1)
			defer active.Add(-1)
			for previous := peak.Load(); count > previous && !peak.CompareAndSwap(previous, count); previous = peak.Load() {
			}
			finish := make(chan struct{})
			started <- finish
			select {
			case <-finish:
			case <-unblock:
			case <-r.Context().Done():
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"topics\":[]}"}}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	defer close(unblock)
	a, err := New(nil, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	for i := range 4 {
		a.sessions = append(a.sessions, sessions.Session{
			ID: fmt.Sprintf("session-%d", i), Agent: "codex", Updated: updated,
			Messages: []sessions.Message{{ID: "question", Role: "user", Text: "请排查这次任务的异常现象。", Timestamp: updated}},
		})
	}
	a.revisions = reviewRevisions(a.sessions)
	handler := a.routes(http.NotFoundHandler(), "", "")
	request := func(method, path, body string) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != 200 {
			t.Fatalf("%s %s gave %d: %s", method, path, w.Code, w.Body)
		}
		return w.Body.Bytes()
	}
	draft, _ := json.Marshal(map[string]string{"preset": "gateway", "name": "Test gateway", "model": "review-test", "baseUrl": gateway.URL + "/v1"})
	var profile modelConnection
	if err := json.Unmarshal(request("POST", "/api/connections", string(draft)), &profile); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/review-settings", fmt.Sprintf(`{"connectionId":%q}`, profile.ID))
	nextRequest := func() chan struct{} {
		t.Helper()
		select {
		case finish := <-started:
			return finish
		case <-time.After(3 * time.Second):
			t.Fatalf("only %d model requests overlapped; want 3", peak.Load())
			return nil
		}
	}
	first, second, third := nextRequest(), nextRequest(), nextRequest()
	window := url.Values{"from": {"2026-04-02T00:00:00+08:00"}, "to": {"2026-04-04T00:00:00+08:00"}}
	reviewURL := "/api/reviews?" + window.Encode()
	pending := string(request("GET", reviewURL, ""))
	if strings.Count(pending, `"state":"running"`) != 3 || strings.Count(pending, `"state":"queued"`) != 1 {
		t.Fatalf("expected three running sessions and one queued: %s", pending)
	}
	close(first)
	fourth := nextRequest()
	close(second)
	close(third)
	close(fourth)
	for deadline := time.Now().Add(3 * time.Second); ; {
		state := string(request("GET", reviewURL, ""))
		if strings.Count(state, `"state":"ready"`) == 4 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected all four sessions to complete: %s", state)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if peak.Load() != 3 {
		t.Fatalf("peak concurrency %d; want 3", peak.Load())
	}
}

func TestAutomaticReviewResumesPersistedBatches(t *testing.T) {
	item := reviewTestSession()
	payment := distill.Topic{Title: "支付回调", Problem: "核对处理结果", Outcome: "已复现重复入账", Status: "verified", Evidence: []string{"old-user", "old-answer"}}
	cache := distill.Topic{Title: "缓存失效", Problem: "核对处理结果", Approach: "先观察缓存命中", Status: "open", Evidence: []string{"old-user", "old-answer"}}
	update := distill.Topic{Title: cache.Title, Problem: cache.Problem, Outcome: "新条目按期淘汰", Status: "verified", Evidence: []string{"old-user", "continuation"}}
	index := distill.Topic{Title: "索引检查", Problem: "核实索引的覆盖范围", Status: "open", Evidence: []string{"next-user"}}
	followup := distill.Topic{Title: index.Title, Problem: index.Problem, Outcome: "确认索引覆盖主键", Status: "verified", Evidence: []string{"followup-user", "followup-answer"}}
	started, reply := make(chan struct{}), make(chan struct{})
	gateway, calls := reviewTestGateway(t, []reviewTestBatch{
		{ids: []string{"old-user", "continuation"}, topics: []distill.Topic{update}, beforeReply: func(ctx context.Context) {
			close(started)
			select {
			case <-reply:
			case <-ctx.Done():
			}
		}},
		{ids: []string{"next-user"}, status: http.StatusServiceUnavailable},
		{ids: []string{"next-user"}, topics: []distill.Topic{index}},
		{ids: []string{"followup-user", "followup-answer"}, topics: []distill.Topic{followup}},
	})
	data := t.TempDir()
	a := openReviewTestApp(t, data, item)
	profile := configureReviewTest(t, a, gateway)
	insertLegacyReview(t, a, item, []distill.Topic{payment, cache})

	a.processReviews(context.Background(), false)
	if calls.Load() != 0 {
		t.Fatal("a current legacy review should be upgraded without asking the model")
	}
	item.Messages = append(item.Messages,
		sessions.Message{ID: "ack", Role: "user", Text: "谢谢。", Timestamp: item.Updated},
		sessions.Message{ID: "continuation", Role: "assistant", Text: "补跑了 TTL 的用例，更新后的数据到期后会被清理。", Timestamp: item.Updated},
		sessions.Message{ID: "noise", Role: "tool", Text: "ignore me", Timestamp: item.Updated},
		sessions.Message{ID: "next-user", Role: "user", Text: "帮我看看查询涉及的字段是否均有合适的索引？", Timestamp: item.Updated},
	)
	setReviewTestSession(a, item)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.processReviews(ctx, false)
	}()
	defer func() { cancel(); <-done }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("append review did not reach the model")
	}
	if entry := readReviewTestEntry(t, a); entry.State != "running" || len(entry.Topics) != 2 || entry.Topics[0].Outcome != payment.Outcome {
		t.Fatalf("running append review should show verified previous topics: %+v", entry)
	}
	if report := reviewTestRequest(t, a, "GET", "/api/report?"+reviewTestWindow, ""); !strings.Contains(report, payment.Outcome) {
		t.Fatalf("running append should retain previous topics in the report: %s", report)
	}
	close(reply)
	<-done
	if entry := readReviewTestEntry(t, a); entry.State != "error" || len(entry.Topics) != 2 || entry.Topics[1].Outcome != update.Outcome {
		t.Fatalf("failed later batch should retain the completed continuation: %+v", entry)
	}
	// Reopen the real database, then drive the coordinator for one run.
	setReviewTestModel(t, a, "")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openReviewTestApp(t, data, item)
	setReviewTestModel(t, reopened, profile.ID)
	reopened.processReviews(context.Background(), false)
	entry := readReviewTestEntry(t, reopened)
	if calls.Load() != 3 || entry.State != "ready" || len(entry.Topics) != 3 ||
		!slices.Equal(entry.Topics[0].Evidence, payment.Evidence) ||
		!slices.Equal(entry.Topics[1].Evidence, []string{"old-user", "old-answer", "continuation"}) ||
		entry.Topics[1].Approach != cache.Approach || entry.Topics[1].Outcome != update.Outcome || entry.Topics[2].Title != index.Title {
		t.Fatalf("expected both earlier topics and the resumed topic after three model calls, got %d: %+v", calls.Load(), entry)
	}
	item.Messages = append(item.Messages,
		sessions.Message{ID: "followup-user", Role: "user", Text: "可以先从刚才提到的主键字段开始看。", Timestamp: item.Updated},
		sessions.Message{ID: "followup-answer", Role: "assistant", Text: "查询计划显示主键字段已经命中。", Timestamp: item.Updated},
	)
	setReviewTestSession(reopened, item)
	reopened.processReviews(context.Background(), false)
	entry = readReviewTestEntry(t, reopened)
	if calls.Load() != 4 || entry.State != "ready" || len(entry.Topics) != 3 ||
		entry.Topics[2].Outcome != followup.Outcome ||
		!slices.Equal(entry.Topics[2].Evidence, []string{"next-user", "followup-user", "followup-answer"}) {
		t.Fatalf("expected a later user follow-up to update its earlier topic: %+v", entry)
	}
}

func TestAutomaticReviewRebuildsUnverifiedHistory(t *testing.T) {
	for _, scenario := range []string{"rewritten prefix", "truncated prefix", "stale legacy array"} {
		t.Run(scenario, func(t *testing.T) {
			item := reviewTestSession()
			old := distill.Topic{Title: "旧问题", Problem: "核对已经废弃的支付方案", Status: "open", Evidence: []string{"old-user"}}
			newTopic := distill.Topic{Title: "新问题", Problem: "确认服务端生效条件", Status: "open", Evidence: []string{"old-user"}}
			a := openReviewTestApp(t, t.TempDir(), item)
			revision := reviewRevision(item.Messages)
			if scenario == "stale legacy array" {
				insertLegacyReview(t, a, item, []distill.Topic{old})
			} else if _, err := a.saveReview(context.Background(), item.ID, revision, reviewCheckpoint{Consumed: len(item.Messages), Topics: []distill.Topic{old}}, storedReview{}); err != nil {
				t.Fatal(err)
			}
			item.Messages = slices.Clone(item.Messages)
			switch scenario {
			case "rewritten prefix":
				item.Messages[0].Text = "请检查服务器端的配置在什么条件下才会启用。"
				item.Messages = append(item.Messages, sessions.Message{ID: "new-answer", Role: "assistant", Text: "正在读取当前项目配置。", Timestamp: item.Updated})
			case "truncated prefix":
				item.Messages = item.Messages[:1]
			case "stale legacy array":
				item.Messages = append(item.Messages, sessions.Message{ID: "new-answer", Role: "assistant", Text: "正在读取当前项目配置。", Timestamp: item.Updated})
			}
			setReviewTestSession(a, item)
			if entry := readReviewTestEntry(t, a); len(entry.Topics) != 0 || entry.State == "ready" {
				t.Fatalf("unverified history was shown: %+v", entry)
			}
			for _, path := range []string{"/api/session?id=" + item.ID, "/api/report?" + reviewTestWindow} {
				if response := reviewTestRequest(t, a, "GET", path, ""); strings.Contains(response, old.Problem) {
					t.Fatalf("unverified evidence was exposed by %s: %s", path, response)
				}
			}
			ids := make([]string, 0, len(item.Messages))
			for _, message := range item.Messages {
				ids = append(ids, message.ID)
			}
			gateway, calls := reviewTestGateway(t, []reviewTestBatch{{ids: ids, topics: []distill.Topic{newTopic}}})
			configureReviewTest(t, a, gateway)
			a.processReviews(context.Background(), false)
			if entry := readReviewTestEntry(t, a); calls.Load() != 1 || entry.State != "ready" || len(entry.Topics) != 1 || entry.Topics[0].Title != newTopic.Title {
				t.Fatalf("expected a fresh review of the current transcript: %+v", entry)
			}
		})
	}
}

const reviewTestWindow = "kind=custom&from=2026-04-02T00:00:00Z&to=2026-04-04T00:00:00Z"

func TestReviewCheckpointRejectsStaleWriter(t *testing.T) {
	ctx := context.Background()
	item, dir := reviewTestSession(), t.TempDir()
	first := openReviewTestApp(t, dir, item)
	second := openReviewTestApp(t, dir, item)
	initial, err := first.saveReview(ctx, item.ID, "initial", reviewCheckpoint{Consumed: 2}, storedReview{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.saveReview(ctx, item.ID, "newer", reviewCheckpoint{Consumed: 4}, initial); err != nil {
		t.Fatal(err)
	}
	if _, err := first.saveReview(ctx, item.ID, "older", reviewCheckpoint{Consumed: 3}, initial); !errors.Is(err, errReviewChanged) {
		t.Fatalf("stale writer: got %v, want a checkpoint conflict", err)
	}
	if _, err := first.saveReview(ctx, item.ID, "older", reviewCheckpoint{Consumed: 3}, storedReview{}); !errors.Is(err, errReviewChanged) {
		t.Fatalf("stale first insert: got %v, want a checkpoint conflict", err)
	}
	rows, err := first.readReviews(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if saved := rows[item.ID]; saved.Consumed != 4 || saved.revision != "newer" {
		t.Fatalf("checkpoint regressed: %+v", saved)
	}
}

func reviewTestSession() sessions.Session {
	const updated = "2026-04-03T08:00:00Z"
	return sessions.Session{ID: "checkpoint", Agent: "codex", Title: "回顾检查", Project: "测试", Updated: updated, Messages: []sessions.Message{
		{ID: "old-user", Role: "user", Text: "请同时核对支付回调与缓存过期的处理。", Timestamp: updated},
		{ID: "old-answer", Role: "assistant", Text: "重放了两次支付通知，两次都进入了写账流程，也记录了缓存命中情况。", Timestamp: updated},
	}}
}

func openReviewTestApp(t *testing.T, data string, item sessions.Session) *app {
	t.Helper()
	a, err := New(nil, data, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	setReviewTestSession(a, item)
	return a
}

func setReviewTestSession(a *app, item sessions.Session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sessions = []sessions.Session{item}
	a.revisions = reviewRevisions(a.sessions)
}

func insertLegacyReview(t *testing.T, a *app, item sessions.Session, topics []distill.Topic) {
	t.Helper()
	raw, err := json.Marshal(topics)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO reviews VALUES(?,?,?)`, item.ID, reviewRevision(item.Messages), string(raw)); err != nil {
		t.Fatal(err)
	}
}

func configureReviewTest(t *testing.T, a *app, gateway *httptest.Server) modelConnection {
	t.Helper()
	draft, _ := json.Marshal(modelConnection{Preset: "gateway", Name: "Checkpoint gateway", BaseURL: gateway.URL + "/v1", Model: "review-test", OutputBudget: 384})
	var profile modelConnection
	if err := json.Unmarshal([]byte(reviewTestRequest(t, a, "POST", "/api/connections", string(draft))), &profile); err != nil {
		t.Fatal(err)
	}
	setReviewTestModel(t, a, profile.ID)
	return profile
}

func setReviewTestModel(t *testing.T, a *app, id string) {
	t.Helper()
	if _, err := a.db.Exec(`INSERT OR REPLACE INTO settings VALUES('review_model',?)`, id); err != nil {
		t.Fatal(err)
	}
}

func readReviewTestEntry(t *testing.T, a *app) reviewEntry {
	t.Helper()
	var result struct{ Entries []reviewEntry }
	body := reviewTestRequest(t, a, "GET", "/api/reviews?"+reviewTestWindow, "")
	if err := json.Unmarshal([]byte(body), &result); err != nil || len(result.Entries) != 1 {
		t.Fatalf("unexpected review response: %s (%v)", body, err)
	}
	return result.Entries[0]
}

func reviewTestRequest(t *testing.T, a *app, method, path, body string) string {
	t.Helper()
	w := httptest.NewRecorder()
	a.routes(http.NotFoundHandler(), "", "").ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s returned %d: %s", method, path, w.Code, w.Body)
	}
	return w.Body.String()
}

type reviewTestBatch struct {
	ids         []string
	topics      []distill.Topic
	status      int
	beforeReply func(context.Context)
}

func reviewTestGateway(t *testing.T, batches []reviewTestBatch) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"review-test"}]}`)
		case "/v1/chat/completions":
			index := int(calls.Add(1)) - 1
			if index >= len(batches) {
				t.Errorf("unexpected review request %d", index+1)
				http.Error(w, "unexpected review request", http.StatusBadRequest)
				return
			}
			var body struct{ Messages []struct{ Content string } }
			var messages []distill.Input
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 ||
				json.Unmarshal([]byte(body.Messages[1].Content), &messages) != nil {
				t.Errorf("invalid request body: %+v (%v)", body, err)
				http.Error(w, "invalid body", http.StatusBadRequest)
				return
			}
			batch := batches[index]
			ids := make([]string, 0, len(messages))
			for _, message := range messages {
				ids = append(ids, message.ID)
			}
			if !slices.Equal(ids, batch.ids) {
				t.Errorf("request %d contained message IDs %v, want %v", index+1, ids, batch.ids)
			}
			if batch.beforeReply != nil {
				batch.beforeReply(r.Context())
			}
			if batch.status != 0 {
				http.Error(w, "provider failure", batch.status)
				return
			}
			raw, _ := json.Marshal(map[string]any{"topics": batch.topics})
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, raw)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(gateway.Close)
	return gateway, &calls
}
