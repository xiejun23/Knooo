package studio

import (
	"encoding/json"
	"fmt"
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

func TestReportSynthesisUsesOnlyScopedReviewFacts(t *testing.T) {
	const inWindow = "2026-04-03T08:00:00Z"
	const excluded = "2026-04-04T00:00:00Z"
	fixture := func(id, project, path, wsl, agent string) sessions.Session {
		return sessions.Session{
			ID: id, Project: project, ProjectPath: path, Platform: "wsl", WSL: wsl, Agent: agent, Title: "PRIVATE_SESSION_TITLE", Updated: inWindow,
			Messages: []sessions.Message{
				{ID: id + "-ask", Role: "user", Text: "PRIVATE_RAW_USER", Timestamp: inWindow},
				{ID: id + "-reply", Role: "assistant", Text: "PRIVATE_RAW_REPLY", Timestamp: inWindow},
				{ID: id + "-future", Role: "assistant", Text: "PRIVATE_RAW_FUTURE", Timestamp: excluded},
			},
		}
	}
	api := fixture("private-a", "Atlas API", "/home/demo/private/api", "Ubuntu", "codex")
	queue := fixture("private-b", "Relay Worker", "/home/demo/private/worker", "Ubuntu", "codex")
	archived := fixture("private-archived", "Archive", "/archive", "Ubuntu", "codex")
	otherWSL := fixture("private-location", "Location", api.ProjectPath, "Debian", "codex")
	otherAgent := fixture("private-agent", "Other Agent", "/other-agent", "Ubuntu", "claude")
	a := openReviewTestApp(t, t.TempDir(), api)
	a.sessions = []sessions.Session{api, queue, archived, otherWSL, otherAgent}
	a.revisions = reviewRevisions(a.sessions)
	for _, item := range a.sessions {
		outcome := map[string]string{api.ID: "重复支付已拦截", queue.ID: "重复通知已拦截"}[item.ID]
		if outcome == "" {
			outcome = "EXCLUDED_" + item.ID
		}
		topics := []distill.Topic{{
			Title: "重放去重", Problem: "防止事件重试重复写入", Outcome: outcome, Status: "verified",
			Evidence: []string{item.Messages[0].ID, item.Messages[1].ID},
		}}
		if item.ID == api.ID {
			topics[0].Approach = "采用唯一键，详见 " + api.Project + " " + api.ProjectPath + " " + api.ID + " " + api.Messages[1].ID
			topics = append(topics, distill.Topic{
				Title: "近日切换完成 FUTURE_TITLE", Problem: "确认切换方案", Outcome: "FUTURE_RESULT", Status: "verified",
				Evidence: []string{item.Messages[0].ID, item.Messages[2].ID},
			})
		}
		if item.ID == queue.ID {
			topics[0].Approach = "采用同样的唯一键，也可参照 " + api.Project
		}
		insertLegacyReview(t, a, item, topics)
	}
	if _, err := a.db.Exec(`INSERT INTO archived_sessions(session) VALUES(?)`, archived.ID); err != nil {
		t.Fatal(err)
	}
	var calls, catalogs atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			catalogs.Add(1)
			fmt.Fprint(w, `{"data":[{"id":"report-test","context_window":4096,"max_output_tokens":384}]}`)
		case "/v1/chat/completions":
			call := calls.Add(1)
			var body struct {
				Messages []struct{ Content string }
				Max      int `json:"max_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 || body.Max != 384 {
				t.Errorf("unexpected report request: %+v (%v)", body, err)
				return
			}
			input := body.Messages[1].Content
			var facts []struct {
				Source int
				distill.ReportFact
			}
			if err := json.Unmarshal([]byte(input), &facts); err != nil || len(facts) != 3 ||
				facts[0].Source != 1 || facts[0].Project != "P1" || facts[0].Outcome != "重复支付已拦截" ||
				facts[1].Source != 2 || facts[1].Project != "P2" || facts[1].Outcome != "重复通知已拦截" ||
				facts[2].Source != 3 || facts[2].Problem != "确认切换方案" || facts[2].Status != "" {
				t.Errorf("scope/order of summary facts is wrong: %s (%v)", input, err)
			}
			for _, private := range []string{"PRIVATE_", "private-", "FUTURE_", "EXCLUDED_", "/home/", "Atlas API", "Relay Worker", "messageId", "sessionId"} {
				if strings.Contains(input, private) {
					t.Errorf("provider request leaked %q: %s", private, input)
				}
			}
			content := `{"items":[{"type":"bugfix","title":"重复写入防护","text":"已拦截重复支付。","sources":[1]}]}`
			if call == 2 {
				content = strings.Replace(content, "[1]", "[1,2]", 1)
			} else if call == 3 {
				http.Error(w, "PRIVATE_PROVIDER_ERROR", http.StatusBadGateway)
				return
			} else if call == 4 {
				content = strings.Replace(content, "[1]", "[3]", 1)
			}
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
		default:
			t.Errorf("unexpected provider route %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	draft, _ := json.Marshal(modelConnection{Preset: "gateway", Name: "Report gateway", BaseURL: gateway.URL + "/v1", Model: "report-test"})
	var profile modelConnection
	if err := json.Unmarshal([]byte(reviewTestRequest(t, a, "POST", "/api/connections", string(draft))), &profile); err != nil {
		t.Fatal(err)
	}
	catalogs.Store(0)
	path := "/api/report?" + strings.Replace(reviewTestWindow, "custom", "weekly", 1) + "&agent=codex&location=wsl:Ubuntu"
	request := func(method, path, body string) workReport {
		t.Helper()
		return decodeTestReport(t, []byte(reviewTestRequest(t, a, method, path, body)))
	}
	body := fmt.Sprintf(`{"connectionId":%q}`, profile.ID)
	local := request(http.MethodGet, path+"&connectionId="+profile.ID, "")
	blank := request(http.MethodPost, path, `{"connectionId":""}`)
	filtered := request(http.MethodGet, path+"&project="+url.QueryEscape(api.ProjectPath), "")
	if local.Mode != "local" || blank.Markdown != local.Markdown || local.Coverage != (reportCoverage{Ready: 2, Active: 2, Topics: 3}) ||
		filtered.Coverage.Topics != 2 || strings.Contains(filtered.Markdown, "Relay Worker") || calls.Load() != 0 || catalogs.Load() != 0 {
		t.Fatalf("unexpected local report or traffic: %+v; filtered %+v", local, filtered)
	}
	if len(local.Sections) != 2 || local.Sections[1].Title != "期间记录的问题" || len(local.Sections[0].Items) != 2 ||
		local.Sections[0].Items[0].Title != local.Sections[0].Items[1].Title ||
		!strings.Contains(local.Sections[0].Items[1].Text, "重复通知已拦截") {
		t.Fatalf("local report should keep same-title outcomes from each project: %+v", local)
	}
	model := request(http.MethodPost, path, body)
	wantSources := []string{api.Messages[0].ID, api.Messages[1].ID, queue.Messages[0].ID, queue.Messages[1].ID, api.Messages[0].ID}
	if model.Mode != "model" || model.Summary != "" || model.Coverage != local.Coverage || model.Sections[0].Title != "缺陷排查与修复" || !strings.Contains(model.Notice, "2 项事项") ||
		!slices.Equal(reportSourceIDs(model), wantSources) ||
		!strings.Contains(model.Markdown, "已拦截重复支付") || strings.Contains(model.Markdown, api.ID) {
		t.Fatalf("unexpected model report or sources: %+v", model)
	}
	if source := model.Sections[0].Items[0].Sources[0]; source.EvidenceVersion != messageEvidenceVersion(api.Messages[0]) {
		t.Fatalf("report did not retain the first source's evidence version: %+v", source)
	}
	for range 3 {
		fallback := request(http.MethodPost, path, body)
		if fallback.Mode != "local" || fallback.Notice != "模型综合暂不可用，已使用本地汇总。" ||
			strings.Contains(fallback.Markdown, "PRIVATE_PROVIDER_ERROR") || !slices.Equal(reportSourceIDs(fallback), reportSourceIDs(local)) {
			t.Fatalf("expected generic scoped fallback: %+v", fallback)
		}
	}
	if calls.Load() != 4 {
		t.Fatalf("expected four explicit calls, got %d", calls.Load())
	}
}

func TestReportLocallyCompletesModelOmissions(t *testing.T) {
	item := reviewTestSession()
	item.ProjectPath = "/home/test/private-project"
	item.Messages = nil
	var topics []distill.Topic
	for i := 1; i <= 10; i++ {
		ask, answer := fmt.Sprint("ask-", i), fmt.Sprint("answer-", i)
		item.Messages = append(item.Messages,
			sessions.Message{ID: ask, Role: "user", Text: "核对结果", Timestamp: item.Updated},
			sessions.Message{ID: answer, Role: "assistant", Text: "已核对", Timestamp: item.Updated},
		)
		topics = append(topics, distill.Topic{
			Title: "回归验证", Problem: fmt.Sprint("检查功能 ", i), Outcome: fmt.Sprint("本地结果 ", i),
			Status: "verified", Evidence: []string{ask, answer},
		})
	}
	a := openReviewTestApp(t, t.TempDir(), item)
	insertLegacyReview(t, a, item, topics)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"data":[{"id":"review-test","context_window":4096,"max_output_tokens":384}]}`)
		case "/v1/chat/completions":
			var body struct{ Messages []struct{ Content string } }
			var packed []struct{ Source int }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Messages) != 2 ||
				json.Unmarshal([]byte(body.Messages[1].Content), &packed) != nil || len(packed) != 10 {
				t.Errorf("expected ten scoped facts, got %+v (%v)", body, err)
			}
			var rows []distill.ReportItem
			for i := 1; i <= 6; i++ {
				rows = append(rows, distill.ReportItem{Title: "回归验证", Text: fmt.Sprint("模型结果 ", i), Type: distill.ReportTesting, Sources: []int{i}})
			}
			content, _ := json.Marshal(distill.ReportSynthesis{Items: rows})
			fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
		default:
			http.NotFound(w, r)
		}
	}))
	defer gateway.Close()
	profile := configureReviewTest(t, a, gateway)
	path := "/api/report?" + strings.Replace(reviewTestWindow, "custom", "daily", 1)
	body := fmt.Sprintf(`{"connectionId":%q}`, profile.ID)
	result := decodeTestReport(t, []byte(reviewTestRequest(t, a, http.MethodPost, path, body)))
	if result.Mode != "model" || result.Coverage.Topics != 10 || !strings.Contains(result.Notice, "4 项事项") ||
		len(result.Sections) != 1 || len(result.Sections[0].Items) != 10 || result.Sections[0].Supplemental ||
		strings.Count(result.Markdown, "模型结果") != 6 || strings.Count(result.Markdown, "本地结果") != 4 || len(reportSourceIDs(result)) != 20 {
		t.Fatalf("expected six model rows and all four local omissions, with evidence: %+v", result)
	}
}

func TestReportKeepsUnansweredQuestionsSeparateFromFollowUps(t *testing.T) {
	question := distill.Topic{Title: "排查导入失败", Problem: "为什么导入会失败？", Status: "open", Evidence: []string{"ask"}}
	topic, ok := reportTopic(question, map[string]string{"ask": "user"}, map[string]int{"ask": 0})
	if !ok || reportTypeFor(topic) != distill.ReportQuestion || reportStatus(topic.Status) != "待确认" ||
		topic.Title != question.Problem || !slices.Equal(topic.Evidence, question.Evidence) {
		t.Fatalf("expected an unanswered question with its original evidence: %+v", topic)
	}
}

func TestReportCombinesOnlyStronglyAssociatedWork(t *testing.T) {
	fact := func(problem, status, outcome string) reportFact {
		return reportFact{topic: distill.Topic{Title: "验证构建", Problem: problem, Status: status, Outcome: outcome},
			sources: []reportSource{{MessageID: outcome}}}
	}
	first := reportFactGroup{project: [3]string{"native", "", "/first"}, facts: []reportFact{
		fact("检查 C++  编译", "verified", "构建通过"),
		fact("检查 C++ 编译", "open", "仍需验证交叉编译"),
		fact("检查 C 编译", "resolved", "已有解决方式"),
		fact("检查 c++ 编译", "resolved", "大小写不同"),
		fact("检查 C++ 编译", "", "问题仍待回复"),
	}}
	second := reportFactGroup{project: [3]string{"native", "", "/second"}, facts: []reportFact{
		fact("检查 C++ 编译", "verified", "另一项目已验证"),
	}}
	unknown := reportFactGroup{facts: []reportFact{
		fact("检查 C++ 编译", "open", "未识别项目的待办"),
		fact("检查 C++ 编译", "verified", "未识别项目的结论"),
	}}
	facts := prepareReportFacts([]reportFactGroup{first, second, unknown}, false)
	if facts[0].model.Work == 0 || facts[0].model.Work != facts[3].model.Work ||
		facts[0].model.Work == facts[1].model.Work || facts[0].model.Work == facts[5].model.Work || facts[0].model.Work == facts[6].model.Work ||
		facts[2].model.Work != 0 || facts[4].model.Work != 0 || facts[7].model.Work != 0 {
		t.Fatalf("project and original problem must match to associate work: %+v", facts)
	}
	local, _ := reportSections(facts, nil, 15)
	model, _ := reportSections(facts, []distill.ReportItem{{Title: "验证构建", Text: "构建通过。", Type: distill.ReportTesting, Sources: []int{1, 4}}}, 15)
	single, _ := reportSections(facts, []distill.ReportItem{{Title: "验证构建", Text: "后续处理。", Type: distill.ReportTesting, Sources: []int{4}}}, 15)
	for _, sections := range [][]reportSection{local, model, single} {
		item := sections[0].Items[0]
		if !strings.Contains(item.Status, "待跟进") || !strings.Contains(item.Text, "仍需验证交叉编译") ||
			len(reportSourceIDs(workReport{Sections: sections})) != 8 {
			t.Fatalf("expected pending detail and all evidence: %+v", sections)
		}
	}
	if row := local[0].Items[0]; row.Status != "已验证、待跟进" || !strings.Contains(row.Text, "构建通过") || len(row.Sources) != 2 {
		t.Fatalf("expected one local row for strongly associated work: %+v", row)
	}
}

func TestReportDemoAndLocalLength(t *testing.T) {
	a, err := New(nil, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer gateway.Close()
	profile := modelConnection{ID: strings.Repeat("A", 26), Preset: "gateway", Name: "Report gateway", BaseURL: gateway.URL + "/v1", Model: "report-test"}
	settings, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.db.Exec(`INSERT INTO settings VALUES(?,?)`, "model:"+profile.ID, string(settings)); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	path := "/api/report?" + url.Values{
		"kind": {"daily"}, "from": {now.AddDate(0, 0, -14).Format(time.RFC3339)}, "to": {now.AddDate(0, 0, 1).Format(time.RFC3339)},
	}.Encode()
	body := fmt.Sprintf(`{"connectionId":%q}`, profile.ID)
	result := decodeTestReport(t, []byte(reviewTestRequest(t, a, http.MethodPost, path, body)))
	var count int
	for _, section := range result.Sections {
		count += len(section.Items)
	}
	if result.Mode != "local" || !strings.Contains(result.Notice, "演示工作区") || count == 0 || count > result.Coverage.Topics || calls.Load() != 0 {
		t.Fatalf("expected a local demo report without provider traffic: %+v", result)
	}
}

func TestCustomReportLayoutUsesCalendarDays(t *testing.T) {
	for _, test := range []struct{ from, to, layout string }{
		{"2026-10-31T00:00:00-04:00", "2026-11-03T00:00:00-05:00", "daily"},
		{"2026-02-20T00:00:00-05:00", "2026-03-20T00:00:00-04:00", "monthly"},
	} {
		from, err := time.Parse(time.RFC3339, test.from)
		if err != nil {
			t.Fatal(err)
		}
		to, err := time.Parse(time.RFC3339, test.to)
		if err != nil {
			t.Fatal(err)
		}
		layout, _ := reportLayoutFor("custom", from, to)
		if layout.focus != reportLayouts[test.layout].focus {
			t.Fatalf("got %+v for %s to %s, want %s", layout, test.from, test.to, test.layout)
		}
	}
}

func TestLocalReportKeepsDailyOverflowAndEvidence(t *testing.T) {
	var group reportFactGroup
	for i := 1; i <= 17; i++ {
		id := fmt.Sprintf("source-%d", i)
		group.facts = append(group.facts, reportFact{
			topic:   distill.Topic{Title: "回归完成", Outcome: "已验证 " + id, Status: "verified"},
			sources: []reportSource{{SessionID: "session", MessageID: id}},
		})
	}
	for i := 18; i <= 45; i++ {
		group.facts[16].sources = append(group.facts[16].sources, reportSource{SessionID: "session", MessageID: fmt.Sprintf("source-%d", i)})
	}
	sections, local := reportSections(prepareReportFacts([]reportFactGroup{group}, false), nil, reportLayouts["daily"].limit)
	if local != 17 || len(sections) != 2 || len(sections[0].Items) != 15 || sections[0].Supplemental ||
		len(sections[1].Items) != 2 || !sections[1].Supplemental || len(reportSourceIDs(workReport{Sections: sections})) != 45 ||
		!strings.Contains(sections[1].Items[1].Text, "source-17") {
		t.Fatalf("expected all daily overflow and evidence: %+v", sections)
	}
}

func TestReportMarkdownEscapesSummary(t *testing.T) {
	for _, test := range []struct{ text, escaped string }{
		{"---", `\-\-\-`},
		{"1. 摘要", `1\. 摘要`},
	} {
		now := time.Now()
		markdown := renderWorkReport(workReport{Title: "日报", Summary: test.text}, now, now.AddDate(0, 0, 1))
		if !strings.Contains(markdown, "\n\n"+test.escaped+"\n") {
			t.Fatalf("expected escaped summary in %s", markdown)
		}
	}
}

func TestReportDocumentKeepsDraftAcrossGenerationAndRestart(t *testing.T) {
	data := t.TempDir()
	item := reviewTestSession()
	initialApp := openReviewTestApp(t, data, item)
	query := url.Values{
		"kind": {"weekly"}, "from": {"2026-04-01T00:00:00Z"}, "to": {"2026-04-08T00:00:00Z"},
		"agent": {"codex", "claude"},
	}
	generate := "/api/report?" + query.Encode()
	query["agent"] = []string{"claude", "codex"}
	path := "/api/report/document?" + query.Encode()

	read := func(a *app) reportDocument {
		t.Helper()
		var doc reportDocument
		raw := reviewTestRequest(t, a, "GET", path, "")
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("cannot read saved report: %s (%v)", raw, err)
		}
		return doc
	}
	write := func(a *app, draft *workReport, draftRevision, reportRevision int64) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"draft": draft, "draftRevision": draftRevision, "reportRevision": reportRevision})
		if err != nil {
			t.Fatal(err)
		}
		reviewTestRequest(t, a, http.MethodPost, path, string(body))
	}

	initial := decodeTestReport(t, []byte(reviewTestRequest(t, initialApp, http.MethodPost, generate, `{"connectionId":""}`)))
	first := read(initialApp)
	if first.ReportRevision != 1 || first.DraftRevision != 0 || string(first.Draft) != "null" {
		t.Fatalf("unexpected new document: %+v", first)
	}
	initial.Title = "手工编辑的周报"
	reviewTestRequest(t, initialApp, http.MethodPost, generate, `{"connectionId":""}`)
	write(initialApp, &initial, first.DraftRevision, first.ReportRevision)
	reviewTestRequest(t, initialApp, http.MethodPost, generate, `{"connectionId":""}`)
	if err := initialApp.Close(); err != nil {
		t.Fatal(err)
	}

	reopened := openReviewTestApp(t, data, item)
	doc := read(reopened)
	if doc.ReportRevision != 3 || doc.DraftRevision != 1 ||
		decodeTestReport(t, doc.Report).Title != "工作周报" || decodeTestReport(t, doc.Draft).Title != initial.Title {
		t.Fatalf("reopening changed a generated version or the draft: %+v", doc)
	}
	write(reopened, nil, doc.DraftRevision, doc.ReportRevision)
	restored := read(reopened)
	if restored.ReportRevision != 3 || restored.DraftRevision != 2 || string(restored.Draft) != "null" {
		t.Fatalf("restoring the generated report changed unexpected fields: %+v", restored)
	}
}

func decodeTestReport(t *testing.T, raw []byte) workReport {
	t.Helper()
	var report workReport
	if err := json.Unmarshal(raw, &report); err != nil || report.Sections == nil || report.Markdown == "" || report.Title == "" {
		t.Fatalf("invalid report: %s (%v)", raw, err)
	}
	return report
}

func reportSourceIDs(report workReport) []string {
	var ids []string
	for _, section := range report.Sections {
		for _, item := range section.Items {
			for _, source := range item.Sources {
				ids = append(ids, source.MessageID)
			}
		}
	}
	return ids
}
