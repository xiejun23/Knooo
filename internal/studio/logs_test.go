package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestPlatformLogsPersistReviewRejection(t *testing.T) {
	item := reviewTestSession()
	topic := distill.Topic{
		Title: "回调核对", Problem: "确认支付通知的处理情况", Approach: "重复投递观察写账路径",
		Status: "open", Evidence: []string{"old-user"},
	}
	gateway, _ := reviewTestGateway(t, []reviewTestBatch{
		{ids: []string{"old-user", "old-answer"}, topics: []distill.Topic{topic}},
		{ids: []string{"old-user", "old-answer"}, topics: []distill.Topic{topic}},
	})
	data := t.TempDir()
	a := openReviewTestApp(t, data, item)
	configureReviewTest(t, a, gateway)
	a.processReviews(context.Background(), false)
	a.recordEvent(platformEvent{Area: "scan", Level: "info", Action: "扫描会话来源"})
	setReviewTestModel(t, a, "")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openReviewTestApp(t, data, item)
	const addr = "127.0.0.1:48763"
	handler := reopened.Handler(addr, "test-token", http.NotFoundHandler(), "", "", nil, nil)
	request := func(query, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "http://"+addr+"/api/logs"+query, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	const filter = "?area=review&level=error"
	if response := request(filter, ""); response.Code != http.StatusForbidden {
		t.Fatalf("unauthorized log request got %d", response.Code)
	}
	if response := request("?area=unknown", "test-token"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid area got %d", response.Code)
	}
	if response := request("?from=not-a-date", "test-token"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid from got %d", response.Code)
	}
	if response := request("?page=0", "test-token"); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid page got %d", response.Code)
	}
	if response := request("?pageSize=999", "test-token"); response.Code != http.StatusBadRequest {
		t.Fatalf("oversized pageSize got %d", response.Code)
	}
	response := request(filter, "test-token")
	var payload platformLogsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || response.Code != http.StatusOK ||
		payload.Total != 1 || len(payload.Events) != 1 {
		t.Fatalf("expected one saved review error, got %d %s (%v)", response.Code, response.Body, err)
	}
	event := payload.Events[0]
	if event.SessionID != item.ID || event.Action != "回顾内容校验失败" ||
		!strings.Contains(event.Message, "第 1 个问题：缺少支持做法或结果的 Agent 回复依据") ||
		!strings.Contains(event.Message, "协议：本地网关 · Chat Completions") ||
		!strings.Contains(event.Message, "模型：review-test") {
		t.Fatalf("missing session or precise rejection: %+v", event)
	}
	if payload.Counts.All < 2 || payload.Counts.Levels["error"] != 1 || payload.Counts.Areas["review"] != 1 {
		t.Fatalf("unexpected facet counts: %+v", payload.Counts)
	}
	for _, transcript := range item.Messages {
		if strings.Contains(response.Body.String(), transcript.Text) {
			t.Fatal("platform log contains transcript text")
		}
	}
}

func TestPlatformLogsDiagnoseReviewModelFailures(t *testing.T) {
	const privateReply = "private-provider-reply"
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   []string
	}{
		{"authentication", http.StatusUnauthorized, privateReply, []string{"HTTP 401", "API 密钥"}},
		{"response envelope", http.StatusOK, `{"content":[{"type":"text","text":"` + privateReply + `"}]}`, []string{"接口响应格式", "choices"}},
		{"review output", http.StatusOK, fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, privateReply), []string{"工作回顾 JSON", "topics"}},
		{"output limit", http.StatusOK, fmt.Sprintf(`{"choices":[{"finish_reason":"length","message":{"content":%q}}]}`, privateReply), []string{"输出上限", "请求上限"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/models" {
					fmt.Fprint(w, `{"data":[{"id":"review-test"}]}`)
					return
				}
				if r.URL.Path != "/v1/chat/completions" {
					t.Errorf("unexpected model path: %s", r.URL.Path)
				}
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			defer gateway.Close()
			item := reviewTestSession()
			a := openReviewTestApp(t, "", item)
			configureReviewTest(t, a, gateway)
			a.processReviews(context.Background(), false)

			raw := reviewTestRequest(t, a, http.MethodGet, "/api/logs?area=review&level=error", "")
			var payload platformLogsResponse
			if err := json.Unmarshal([]byte(raw), &payload); err != nil || len(payload.Events) != 1 {
				t.Fatalf("expected one review error: %s (%v)", raw, err)
			}
			event := payload.Events[0]
			for _, want := range append(test.want, "协议：本地网关 · Chat Completions", "模型：review-test", gateway.URL) {
				if !strings.Contains(event.Message, want) {
					t.Errorf("review diagnostics missing %q in %q", want, event.Message)
				}
			}
			if event.SessionID != item.ID || strings.Contains(raw, privateReply) || strings.Contains(raw, gateway.URL+"/v1") {
				t.Fatalf("unexpected review log contents: %s", raw)
			}
		})
	}
}

func TestPlatformLogsKeepNewestEventsAndPaginate(t *testing.T) {
	a := openReviewTestApp(t, "", reviewTestSession())
	for range 1002 {
		a.recordEvent(platformEvent{Area: "scan", Level: "info", Action: "扫描会话来源"})
	}
	var firstPage platformLogsResponse
	body := reviewTestRequest(t, a, http.MethodGet, "/api/logs?page=1&pageSize=20", "")
	if err := json.Unmarshal([]byte(body), &firstPage); err != nil || firstPage.Total != 1000 ||
		len(firstPage.Events) != 20 || firstPage.Events[0].ID != 1002 || firstPage.Events[19].ID != 983 {
		t.Fatalf("expected page 1 of newest 1000 events, got total=%d len=%d (%v)", firstPage.Total, len(firstPage.Events), err)
	}
	var lastPage platformLogsResponse
	body = reviewTestRequest(t, a, http.MethodGet, "/api/logs?page=50&pageSize=20", "")
	if err := json.Unmarshal([]byte(body), &lastPage); err != nil || lastPage.Page != 50 ||
		len(lastPage.Events) != 20 || lastPage.Events[len(lastPage.Events)-1].ID != 3 {
		t.Fatalf("expected last page ending at id 3, got page=%d len=%d (%v)", lastPage.Page, len(lastPage.Events), err)
	}
	var clamped platformLogsResponse
	body = reviewTestRequest(t, a, http.MethodGet, "/api/logs?page=999&pageSize=20", "")
	if err := json.Unmarshal([]byte(body), &clamped); err != nil || clamped.Page != 50 || len(clamped.Events) != 20 {
		t.Fatalf("expected out-of-range page to clamp to 50, got page=%d len=%d (%v)", clamped.Page, len(clamped.Events), err)
	}
}

func TestPlatformLogsTimeRangeKeywordAndFacets(t *testing.T) {
	a := openReviewTestApp(t, "", reviewTestSession())
	t1 := time.Date(2026, 11, 20, 8, 30, 0, 0, time.UTC)
	t2 := time.Date(2026, 11, 24, 16, 0, 0, 500000000, time.UTC)
	t3 := time.Date(2026, 11, 25, 2, 15, 0, 0, time.UTC)

	// Insert one legacy RFC3339Nano row without fractional seconds to verify boundary comparison.
	if _, err := a.db.Exec(`INSERT INTO platform_logs (time,area,level,action,session,message,retry_at) VALUES (?,?,?,?,?,?,?)`,
		t1.Format(time.RFC3339Nano), "scan", "info", "扫描会话来源", "", "旧记录", ""); err != nil {
		t.Fatal(err)
	}
	a.recordEventAt(t2, platformEvent{Area: "model", Level: "warning", Action: "模型请求触发限流", Message: "HTTP 429"})
	a.recordEventAt(t3, platformEvent{Area: "review", Level: "error", Action: "回顾内容校验失败", SessionID: "sess-abc", Message: "缺少回复依据"})

	// Query from 2026-11-24T16:00:00Z to 2026-11-25T16:00:00Z (equivalent to 2026-11-25 local +08:00 day).
	q := url.Values{
		"from":     {"2026-11-25T00:00:00+08:00"},
		"to":       {"2026-11-26T00:00:00+08:00"},
		"pageSize": {"10"},
	}
	var rangeRes platformLogsResponse
	body := reviewTestRequest(t, a, http.MethodGet, "/api/logs?"+q.Encode(), "")
	if err := json.Unmarshal([]byte(body), &rangeRes); err != nil || rangeRes.Total != 2 || len(rangeRes.Events) != 2 {
		t.Fatalf("expected 2 events in time range, got total=%d len=%d (%v)", rangeRes.Total, len(rangeRes.Events), err)
	}
	if rangeRes.Counts.All != 2 || rangeRes.Counts.Levels["error"] != 1 || rangeRes.Counts.Levels["warning"] != 1 ||
		rangeRes.Counts.Areas["review"] != 1 || rangeRes.Counts.Areas["model"] != 1 {
		t.Fatalf("unexpected time-scoped counts: %+v", rangeRes.Counts)
	}

	// Keyword filter matching session ID case-insensitively.
	q.Set("q", "SESS-ABC")
	var keywordRes platformLogsResponse
	body = reviewTestRequest(t, a, http.MethodGet, "/api/logs?"+q.Encode(), "")
	if err := json.Unmarshal([]byte(body), &keywordRes); err != nil || keywordRes.Total != 1 ||
		keywordRes.Events[0].SessionID != "sess-abc" {
		t.Fatalf("expected keyword match on session id, got %+v (%v)", keywordRes, err)
	}
}

func TestPlatformLogsRecordModelLifecycleWithoutLeakingSecrets(t *testing.T) {
	const secretKey = "sk-super-secret-value-12345"
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			jsonReply(w, map[string]any{"data": []map[string]string{{"id": "deepseek-chat"}, {"id": "deepseek-reasoner"}}})
		case "/v1/chat/completions":
			jsonReply(w, map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": "ok"}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()

	a := openReviewTestApp(t, "", reviewTestSession())

	createdBody := reviewTestRequest(t, a, http.MethodPost, "/api/connections",
		`{"preset":"gateway","gatewayApi":"chat","name":"本地测试网关","baseUrl":"`+gateway.URL+`/v1","model":"deepseek-chat","apiKey":"`+secretKey+`"}`)
	var created modelConnection
	if err := json.Unmarshal([]byte(createdBody), &created); err != nil || created.ID == "" {
		t.Fatalf("create connection failed: %s (%v)", createdBody, err)
	}

	reviewTestRequest(t, a, http.MethodPost, "/api/models", `{"id":"`+created.ID+`","test":true}`)
	reviewTestRequest(t, a, http.MethodPost, "/api/models", `{"id":"`+created.ID+`"}`)

	reviewTestRequest(t, a, http.MethodPost, "/api/connections",
		`{"id":"`+created.ID+`","preset":"gateway","gatewayApi":"chat","name":"本地测试网关","baseUrl":"`+gateway.URL+`/v1","model":"deepseek-reasoner"}`)

	reviewTestRequest(t, a, http.MethodPost, "/api/connections",
		`{"id":"`+created.ID+`","preset":"gateway","gatewayApi":"chat","name":"本地测试网关","baseUrl":"`+gateway.URL+`/v1","model":"deepseek-reasoner","disabled":true}`)
	reviewTestRequest(t, a, http.MethodPost, "/api/connections", `{"id":"`+created.ID+`","remove":true}`)

	rawLogs := reviewTestRequest(t, a, http.MethodGet, "/api/logs?area=model&pageSize=20", "")
	if strings.Contains(rawLogs, secretKey) {
		t.Fatalf("platform logs leaked API key: %s", rawLogs)
	}
	var payload platformLogsResponse
	if err := json.Unmarshal([]byte(rawLogs), &payload); err != nil {
		t.Fatal(err)
	}
	actions := make([]string, 0, len(payload.Events))
	for _, ev := range payload.Events {
		actions = append(actions, ev.Action)
		if !strings.Contains(ev.Message, "本地测试网关") || !strings.Contains(ev.Message, gateway.URL) ||
			strings.Contains(ev.Message, gateway.URL+"/v1") {
			t.Fatalf("expected connection identity and origin in %q, got: %s", ev.Action, ev.Message)
		}
	}
	wantActions := []string{"删除模型连接", "停用模型连接", "更新模型连接", "读取模型列表", "模型连接测试通过", "新建模型连接"}
	if strings.Join(actions, ",") != strings.Join(wantActions, ",") {
		t.Fatalf("unexpected model actions order: got %v, want %v", actions, wantActions)
	}
	if !strings.Contains(payload.Events[2].Message, "deepseek-chat → deepseek-reasoner") {
		t.Fatalf("expected model diff in update log, got: %s", payload.Events[2].Message)
	}
	if !strings.Contains(payload.Events[3].Message, "返回 2 个可用模型") || !strings.Contains(payload.Events[3].Message, "deepseek-chat") {
		t.Fatalf("expected model count and sample in list log, got: %s", payload.Events[3].Message)
	}
}

func TestPlatformLogsRecordDetailedSessionScan(t *testing.T) {
	isolateSessionEnvironment(t)
	home := t.TempDir()
	codexDir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(codexDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeCodex := func(name, id, cwd, prompt string) {
		t.Helper()
		content := strings.Join([]string{
			`{"timestamp":"2026-11-25T10:00:00Z","type":"session_meta","payload":{"id":"` + id + `","cwd":"` + cwd + `"}}`,
			`{"timestamp":"2026-11-25T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + prompt + `"}]}}`,
			`{"timestamp":"2026-11-25T10:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"已完成核对。"}]}}`,
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(codexDir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeCodex("s1.jsonl", "scan-1", "/work/demo", "检查扫描日志详情")

	a, err := New(sessions.New(home), "", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })

	// 1. Initial scan.
	a.ensureSessionIndex()
	// 2. Background poll with no change should NOT add a log.
	reviewTestRequest(t, a, http.MethodPost, "/api/refresh", `{}`)
	// 3. Manual refresh with no change SHOULD add a log.
	reviewTestRequest(t, a, http.MethodPost, "/api/refresh", `{"manual":true}`)
	// 4. Incremental scan with 1 added session.
	writeCodex("s2.jsonl", "scan-2", "/work/demo", "新增第二条会话")
	reviewTestRequest(t, a, http.MethodPost, "/api/refresh", `{}`)

	raw := reviewTestRequest(t, a, http.MethodGet, "/api/logs?area=scan&pageSize=10", "")
	var payload platformLogsResponse
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Total != 3 {
		t.Fatalf("expected 3 scan logs (startup + manual + incremental), got %d: %+v", payload.Total, payload.Events)
	}
	if payload.Events[0].Action != "增量扫描发现会话变更" || !strings.Contains(payload.Events[0].Message, "新增 1 条") ||
		!strings.Contains(payload.Events[0].Message, "Codex 2 条") {
		t.Fatalf("unexpected incremental scan log: %+v", payload.Events[0])
	}
	if payload.Events[1].Action != "手动刷新会话来源" || !strings.Contains(payload.Events[1].Message, "无会话变更") ||
		!strings.Contains(payload.Events[1].Message, "Codex 1 条") {
		t.Fatalf("unexpected manual scan log: %+v", payload.Events[1])
	}
	if payload.Events[2].Action != "初次扫描会话来源" || !strings.Contains(payload.Events[2].Message, "共发现 1 条会话") ||
		!strings.Contains(payload.Events[2].Message, "发现来源") || !strings.Contains(payload.Events[2].Message, "刷新文件") {
		t.Fatalf("unexpected startup scan log: %+v", payload.Events[2])
	}

	// Verify multi-environment & warning formatting details directly.
	action, detail := formatScanSummary(
		"sources",
		[]sessions.Session{{Agent: "antigravity", AgentVariant: "cli"}, {Agent: "claude"}},
		[]string{"WSL 发行版 Debian 未运行"},
		sessions.ScanReport{
			Environments: []sessions.Environment{
				{Platform: "windows", State: "scanned"},
				{Platform: "wsl", WSL: "Debian", State: "stopped"},
			},
			Roots: []sessions.ScanRoot{{Agent: "claude", Sessions: 1}},
		},
		1, 0, 0, 2, 125*time.Millisecond,
	)
	if action != "更新扫描来源并重新扫描" ||
		!strings.Contains(detail, "已保存 2 个自定义扫描目录") ||
		!strings.Contains(detail, "Antigravity CLI 1 条") ||
		!strings.Contains(detail, "Windows（已扫描）、WSL Debian（已停止）") ||
		!strings.Contains(detail, "WSL 发行版 Debian 未运行") {
		t.Fatalf("unexpected formatted scan detail: action=%q detail=%s", action, detail)
	}
}
