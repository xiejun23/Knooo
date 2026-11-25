package studio

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestAutomaticReviewReportsDatedContributionsAndReopens(t *testing.T) {
	const (
		first  = "2026-04-02T00:00:00Z"
		second = "2026-04-03T00:00:00Z"
		third  = "2026-04-04T00:00:00Z"
		end    = "2026-04-05T00:00:00Z"
	)
	item := reviewTestSession()
	item.Updated = first
	for i := range item.Messages {
		item.Messages[i].Timestamp = first
	}
	initial := distill.Topic{
		Title: "缓存过期", Problem: "核对缓存生命周期",
		Approach: "检查单节点缓存命中率", Outcome: "单节点到期淘汰成功",
		Status: "verified", Evidence: []string{"old-user", "old-answer"},
	}
	update := distill.Topic{
		Title: initial.Title, Problem: initial.Problem, Outcome: "多节点也通过淘汰检查",
		Status: "verified", Evidence: []string{"old-user", "continuation"},
	}
	reopen := distill.Topic{Title: initial.Title, Problem: initial.Problem, Status: "open", Evidence: []string{"reopen"}}
	gateway, calls := reviewTestGateway(t, []reviewTestBatch{
		{ids: update.Evidence, topics: []distill.Topic{update}},
		{ids: reopen.Evidence, topics: []distill.Topic{reopen}},
	})
	data := t.TempDir()
	a := openReviewTestApp(t, data, item)
	profile := configureReviewTest(t, a, gateway)
	insertLegacyReview(t, a, item, []distill.Topic{initial})
	a.processReviews(context.Background(), false)
	item.Messages = append(item.Messages, sessions.Message{
		ID: "continuation", Role: "assistant", Timestamp: second,
		Text: "又跑了三台服务器的测试，每台都正确删除过期条目。",
	})
	item.Updated = second
	setReviewTestSession(a, item)
	a.processReviews(context.Background(), false)

	setReviewTestModel(t, a, "")
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	a = openReviewTestApp(t, data, item)
	setReviewTestModel(t, a, profile.ID)
	item.Messages = append(item.Messages, sessions.Message{
		ID: "reopen", Role: "user", Timestamp: third,
		Text: "换了后端实现，请再检查一下缓存是否还会按时删除。",
	})
	item.Updated = third
	setReviewTestSession(a, item)
	a.processReviews(context.Background(), false)
	if calls.Load() != 2 {
		t.Fatalf("expected two continuation calls, got %d", calls.Load())
	}

	for _, test := range []struct {
		name, from, to string
		section        string
		want, omit     []string
		refs           []string
	}{
		{"initial day", first, second, "本期成果", []string{initial.Outcome}, []string{update.Outcome}, []string{"old-user", "old-answer"}},
		{"continuation day", second, third, "本期成果", []string{update.Outcome}, []string{initial.Approach, initial.Outcome}, []string{"continuation"}},
		{"reopened day", third, end, "阻碍与待办", nil, []string{initial.Approach, initial.Outcome, update.Outcome}, []string{"reopen"}},
		{"before reopening", first, third, "本期成果", []string{update.Outcome}, []string{initial.Outcome}, []string{"old-user", "old-answer", "continuation"}},
		{"full span", first, end, "阻碍与待办", []string{initial.Approach}, []string{initial.Outcome, update.Outcome}, []string{"old-user", "old-answer", "continuation", "reopen"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := reviewTestRequest(t, a, "GET", "/api/report?kind=custom&from="+test.from+"&to="+test.to, "")
			report := decodeTestReport(t, []byte(raw))
			if report.Coverage.Topics != 1 || len(report.Sections) != 1 || report.Sections[0].Title != test.section ||
				len(report.Sections[0].Items) != 1 || report.Sections[0].Items[0].Title != initial.Problem ||
				!slices.Equal(reportSourceIDs(report), test.refs) {
				t.Fatalf("expected one contribution with only in-window evidence: %s", raw)
			}
			for _, value := range test.want {
				if !strings.Contains(report.Markdown, value) {
					t.Fatalf("report missing %q: %s", value, raw)
				}
			}
			for _, value := range test.omit {
				if strings.Contains(report.Markdown, value) {
					t.Fatalf("report unexpectedly contains %q: %s", value, raw)
				}
			}
		})
	}
	detail := reviewTestRequest(t, a, "GET", "/api/session?id="+item.ID, "")
	var result struct{ Topics []distill.Topic }
	if err := json.Unmarshal([]byte(detail), &result); err != nil || len(result.Topics) != 1 {
		t.Fatalf("expected one current topic: %s (%v)", detail, err)
	}
	topic := result.Topics[0]
	if topic.Status != "open" || topic.Outcome != "" || topic.Approach != initial.Approach ||
		!slices.Equal(topic.Evidence, []string{"old-user", "old-answer", "continuation", "reopen"}) ||
		strings.Contains(detail, "reviewHistory") {
		t.Fatalf("expected reopened topic with cumulative evidence: %s", detail)
	}
}

func TestAutomaticReviewRetriesInvalidBatchAfterCheckpoint(t *testing.T) {
	for _, finish := range []bool{true, false} {
		name := "recover"
		if !finish {
			name = "exhaust attempts"
		}
		t.Run(name, func(t *testing.T) {
			item := reviewTestSession()
			item.Messages = append(item.Messages, sessions.Message{
				ID: "next-user", Role: "user", Timestamp: item.Updated,
				Text: "区分下服务端使用的索引和主键字段。",
			})
			initial := distill.Topic{Title: "缓存过期", Problem: "核对缓存生命周期", Status: "open", Evidence: []string{"old-user"}}
			next := distill.Topic{Title: "索引检查", Problem: "梳理查询的字段覆盖范围", Status: "open", Evidence: []string{"next-user"}}
			batches := []reviewTestBatch{{ids: []string{"old-user", "old-answer"}, topics: []distill.Topic{initial}}}
			for i := 0; i < reviewResponseAttempts; i++ {
				batch := reviewTestBatch{ids: next.Evidence}
				if finish && i == 1 {
					batch.topics = []distill.Topic{next}
				}
				batches = append(batches, batch)
			}
			gateway, calls := reviewTestGateway(t, batches)
			a := openReviewTestApp(t, t.TempDir(), item)
			configureReviewTest(t, a, gateway)
			a.processReviews(context.Background(), false)

			cached, err := a.readReviews(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if entry := readReviewTestEntry(t, a); calls.Load() != 2 || entry.State != "error" ||
				len(entry.Topics) != 1 || entry.Topics[0].Title != initial.Title || cached[item.ID].Consumed != 2 {
				t.Fatalf("invalid second batch should leave the first batch saved: %+v, checkpoint: %+v", entry, cached[item.ID])
			}
			for attempt := 1; attempt < reviewResponseAttempts; attempt++ {
				a.processReviews(context.Background(), false)
				if got := calls.Load(); got != int32(attempt+1) {
					t.Fatalf("attempt %d during cooldown made a request: %d", attempt, got)
				}
				a.review.Lock()
				issue := a.review.rejected[item.ID]
				if issue.retryAt.IsZero() || issue.attempts != attempt {
					a.review.Unlock()
					t.Fatalf("expected a cooldown after attempt %d: %+v", attempt, issue)
				}
				issue.retryAt = time.Now().Add(-time.Second)
				a.review.rejected[item.ID] = issue
				a.review.Unlock()
				a.processReviews(context.Background(), false)
				if finish {
					entry := readReviewTestEntry(t, a)
					if calls.Load() != 3 || entry.State != "ready" || len(entry.Topics) != 2 ||
						entry.Topics[0].Title != initial.Title || entry.Topics[1].Title != next.Title {
						t.Fatalf("eligible retry should finish the saved suffix: %+v", entry)
					}
					return
				}
			}
			a.processReviews(context.Background(), false)
			if entry := readReviewTestEntry(t, a); calls.Load() != int32(reviewResponseAttempts+1) ||
				entry.State != "error" || len(entry.Topics) != 1 {
				t.Fatalf("exhausted retry should stop at the saved progress: calls=%d, entry=%+v", calls.Load(), entry)
			}
		})
	}
}

func TestAutomaticReviewShowsRejectionOnlyForErrorEntry(t *testing.T) {
	ctx := context.Background()
	item := reviewTestSession()
	revision := reviewRevision(item.Messages)
	data := t.TempDir()
	a := openReviewTestApp(t, data, item)
	other := openReviewTestApp(t, data, item)
	setReviewTestModel(t, a, "test-model")
	checkpoint, err := a.saveReview(ctx, item.ID, reviewRevision(item.Messages[:1]),
		reviewCheckpoint{Consumed: 1}, storedReview{})
	if err != nil {
		t.Fatal(err)
	}
	const rejection = "模型未返回有效的工作回顾"
	a.review.rejected = map[string]reviewIssue{item.ID: {
		revision: revision, message: rejection, consumed: 1, attempts: 1,
		retryAt: time.Now().Add(-time.Second),
	}}
	check := func(state, message string) {
		t.Helper()
		var result struct {
			Error   string
			Entries []reviewEntry
		}
		body := reviewTestRequest(t, a, "GET", "/api/reviews?"+reviewTestWindow, "")
		if err := json.Unmarshal([]byte(body), &result); err != nil || len(result.Entries) != 1 ||
			result.Entries[0].State != state || result.Error != message {
			t.Errorf("expected state %q and error %q, got %s (%v)", state, message, body, err)
		}
	}
	check("error", rejection)
	a.review.running = map[string]string{item.ID: revision}
	check("running", "")
	a.review.running = nil
	if _, err := other.saveReview(ctx, item.ID, revision,
		reviewCheckpoint{Consumed: len(item.Messages)}, checkpoint); err != nil {
		t.Fatal(err)
	}
	check("ready", "")
}

func TestAutomaticReviewMergesRefinedProblemAcrossBatchesAndConsolidatesHistory(t *testing.T) {
	const first = "2026-04-02T00:00:00Z"
	const second = "2026-04-03T00:00:00Z"
	const end = "2026-04-04T00:00:00Z"

	item := sessions.Session{
		ID: "multi-batch", Agent: "codex", Project: "示例项目", Title: "长会话", Updated: second,
		Messages: []sessions.Message{
			{ID: "u1", Role: "user", Text: "帮我排查订单接口超时并优化查询。", Timestamp: first},
			{ID: "a1", Role: "assistant", Text: "定位到缺少组合索引导致全表扫描。", Timestamp: first},
			{ID: "u2", Role: "user", Text: "顺便确认下配置目录在哪里。", Timestamp: first},
			{ID: "a2", Role: "assistant", Text: "配置文件位于项目根目录的 config.json。", Timestamp: first},
			{ID: "u3", Role: "user", Text: "给订单表加上索引后再压测看看。", Timestamp: second},
			{ID: "a3", Role: "assistant", Text: "已添加组合索引，压测平均耗时从 800ms 降到 20ms。", Timestamp: second},
		},
	}
	initialMain := distill.Topic{
		Title: "优化订单接口查询耗时", Problem: "排查订单接口超时原因",
		Approach: "分析慢查询计划", Outcome: "确认缺少组合索引",
		Status: "open", Evidence: []string{"u1", "a1"},
	}
	sideNote := distill.Topic{
		Title: "确认配置目录位置", Problem: "查找服务配置文件路径",
		Outcome: "位于根目录 config.json", Status: "observed", Evidence: []string{"u2", "a2"},
	}
	// Batch 2 continues initialMain (which did NOT touch u2!) and refines Problem wording.
	refinedUpdate := distill.Topic{
		Title: "优化订单接口查询耗时", Problem: "为订单表添加组合索引并验证压测耗时",
		Approach: "新增组合索引并执行压测", Outcome: "压测延迟由八百毫秒降至二十毫秒，性能达标",
		Status: "verified", Evidence: []string{"u3", "a3"},
	}
	gateway, calls := reviewTestGateway(t, []reviewTestBatch{
		{ids: []string{"u3", "a3"}, topics: []distill.Topic{refinedUpdate}},
	})
	a := openReviewTestApp(t, t.TempDir(), item)
	configureReviewTest(t, a, gateway)
	if _, err := a.saveReview(context.Background(), item.ID, reviewRevision(item.Messages[:4]), reviewCheckpoint{
		Consumed: 4,
		Topics:   []distill.Topic{initialMain, sideNote},
	}, storedReview{}); err != nil {
		t.Fatal(err)
	}

	a.processReviews(context.Background(), false)
	entry := readReviewTestEntry(t, a)
	if calls.Load() != 1 || entry.State != "ready" || len(entry.Topics) != 2 {
		t.Fatalf("expected refined continuation to merge into earlier topic without duplicating: calls=%d entry=%+v", calls.Load(), entry)
	}
	if entry.Topics[0].Status != "verified" || entry.Topics[0].Outcome != refinedUpdate.Outcome ||
		!slices.Equal(entry.Topics[0].Evidence, []string{"u1", "a1", "u3", "a3"}) {
		t.Fatalf("unexpected merged topic: %+v", entry.Topics[0])
	}

	// Historical duplicate rows already stored in SQLite should consolidate on read without downgrading verified status.
	consolidated := distill.ConsolidateReviewTopics([]distill.Topic{
		{Title: "准备并启动 ITSM 项目", Problem: "配置环境并启动项目", Approach: "安装依赖并启动三仓服务", Outcome: "全部服务启动成功", Status: "verified", Evidence: []string{"u1", "a1"}},
		{Title: "准备并启动 ITSM 项目", Problem: "切分支后整理启动流程", Outcome: "补充了分支启动说明", Status: "observed", Evidence: []string{"u2", "a2"}},
	})
	if len(consolidated) != 1 || consolidated[0].Status != "verified" || consolidated[0].Outcome != "全部服务启动成功" ||
		!slices.Equal(consolidated[0].Evidence, []string{"u1", "a1", "u2", "a2"}) || len(consolidated[0].ReviewHistory) != 2 {
		t.Fatalf("unexpected historical consolidation: %+v", consolidated)
	}

	// Dated report on day 2 should still isolate day 2's verified result after merge.
	raw := reviewTestRequest(t, a, "GET", "/api/report?kind=custom&from="+second+"&to="+end, "")
	report := decodeTestReport(t, []byte(raw))
	if !strings.Contains(report.Markdown, refinedUpdate.Outcome) || strings.Contains(report.Markdown, initialMain.Outcome) {
		t.Fatalf("dated report should preserve per-batch history after title merge: %s", raw)
	}
}

func TestAutomaticReviewIdleCooldownAndPersistedFailure(t *testing.T) {
	now := time.Now().UTC()
	recent := now.Add(-30 * time.Second).Format(time.RFC3339)
	idle := now.Add(-10 * time.Minute).Format(time.RFC3339)
	window := "kind=custom&from=" + now.Add(-time.Hour).Format(time.RFC3339) + "&to=" + now.Add(time.Hour).Format(time.RFC3339)

	item := sessions.Session{
		ID: "active-session", Agent: "codex", Project: "示例项目", Title: "排查登录超时", Updated: recent,
		Messages: []sessions.Message{
			{ID: "u1", Role: "user", Text: "排查登录接口超时", Timestamp: recent},
			{ID: "a1", Role: "assistant", Text: "发现连接池配置过小，已调大并验证通过。", Timestamp: recent},
		},
	}
	// Concise title matching the crisp user prompt should be accepted without rejecting.
	conciseTopic := distill.Topic{
		Title: "排查登录接口超时", Problem: "排查登录接口超时",
		Approach: "检查并调大连接池上限", Outcome: "登录接口恢复正常响应",
		Status: "verified", Evidence: []string{"u1", "a1"},
	}
	gateway, calls := reviewTestGateway(t, []reviewTestBatch{
		{ids: []string{"u1", "a1"}, topics: []distill.Topic{conciseTopic}},
	})
	data := t.TempDir()
	a := openReviewTestApp(t, data, item)
	configureReviewTest(t, a, gateway)

	// While active (< 5 min idle), background review must defer without calling the model.
	a.processReviews(context.Background(), false)
	if calls.Load() != 0 {
		t.Fatalf("active session should wait for idle cooldown, got %d calls", calls.Load())
	}
	var res struct{ Entries []reviewEntry }
	body := reviewTestRequest(t, a, "GET", "/api/reviews?"+window, "")
	if err := json.Unmarshal([]byte(body), &res); err != nil || len(res.Entries) != 1 || res.Entries[0].State != "cooldown" {
		t.Fatalf("expected cooldown state for active session, got %s", body)
	}

	// Once idle past reviewIdleDelay, the session processes in a single call and accepts concise title reuse.
	item.Updated = idle
	item.Messages[0].Timestamp, item.Messages[1].Timestamp = idle, idle
	setReviewTestSession(a, item)
	a.processReviews(context.Background(), false)
	if calls.Load() != 1 {
		t.Fatalf("expected one review call once idle, got %d", calls.Load())
	}
	body = reviewTestRequest(t, a, "GET", "/api/reviews?"+window, "")
	if err := json.Unmarshal([]byte(body), &res); err != nil || len(res.Entries) != 1 || res.Entries[0].State != "ready" || len(res.Entries[0].Topics) != 1 {
		t.Fatalf("expected ready concise topic after idle, got %s", body)
	}

	// Exhausted validation failure must persist across restarts and not burn tokens again for the same revision.
	badItem := sessions.Session{
		ID: "stubborn-session", Agent: "codex", Project: "示例项目", Title: "异常会话", Updated: idle,
		Messages: []sessions.Message{
			{ID: "b1", Role: "user", Text: "检查构建脚本的缓存参数。", Timestamp: idle},
		},
	}
	failGateway, failCalls := reviewTestGateway(t, []reviewTestBatch{{ids: []string{"b1"}}})
	failDir := t.TempDir()
	fa := openReviewTestApp(t, failDir, badItem)
	profile := configureReviewTest(t, fa, failGateway)
	fa.processReviews(context.Background(), false)
	if failCalls.Load() != 1 {
		t.Fatalf("expected 1 failed attempt on initial batch, got %d", failCalls.Load())
	}
	setReviewTestModel(t, fa, "")
	if err := fa.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openReviewTestApp(t, failDir, badItem)
	setReviewTestModel(t, reopened, profile.ID)
	reopened.processReviews(context.Background(), false)
	if failCalls.Load() != 1 {
		t.Fatalf("persisted failure should prevent re-calling model after restart, got %d calls", failCalls.Load())
	}
}

func TestAutomaticReviewBacksOffTransientFailureAndProcessesOtherSessions(t *testing.T) {
	idle := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	slow := sessions.Session{
		ID: "slow-session", Agent: "codex", Project: "示例项目", Title: "超长会话", Updated: idle,
		Messages: []sessions.Message{
			{ID: "s1", Role: "user", Text: "分析整库性能瓶颈。", Timestamp: idle},
			{ID: "s2", Role: "assistant", Text: "正在逐模块排查。", Timestamp: idle},
		},
	}
	healthy := sessions.Session{
		ID: "healthy-session", Agent: "codex", Project: "示例项目", Title: "正常会话", Updated: idle,
		Messages: []sessions.Message{
			{ID: "h1", Role: "user", Text: "修复分页参数边界。", Timestamp: idle},
			{ID: "h2", Role: "assistant", Text: "已限制页码下限为 1 并补测通过。", Timestamp: idle},
		},
	}
	okTopic := distill.Topic{
		Title: "修复分页参数边界", Problem: "防止非法页码参数",
		Approach: "限制页码下限并补充测试", Outcome: "分页边界测试通过",
		Status: "verified", Evidence: []string{"h1", "h2"},
	}
	gateway, calls := reviewTestGateway(t, []reviewTestBatch{
		{ids: []string{"s1", "s2"}, status: http.StatusGatewayTimeout},
		{ids: []string{"h1", "h2"}, topics: []distill.Topic{okTopic}},
	})
	a, err := New(nil, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions = []sessions.Session{slow}
	a.revisions = reviewRevisions(a.sessions)
	configureReviewTest(t, a, gateway)

	// First pass: slow session times out and enters backoff.
	a.processReviews(context.Background(), false)
	if calls.Load() != 1 {
		t.Fatalf("expected 1 call on first pass, got %d", calls.Load())
	}
	// Second pass immediately after: slow session is backed off, healthy session proceeds without looping on slow.
	a.sessions = []sessions.Session{slow, healthy}
	a.revisions = reviewRevisions(a.sessions)
	a.processReviews(context.Background(), false)
	if calls.Load() != 2 {
		t.Fatalf("expected slow session to back off and healthy session to run, got %d calls", calls.Load())
	}
	cached, err := a.readReviews(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cached[healthy.ID].Topics) != 1 || cached[healthy.ID].Topics[0].Title != okTopic.Title {
		t.Fatalf("expected healthy session to finish review: %+v", cached[healthy.ID])
	}
}
