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
			topics[0].Approach = "采用唯一键，详见 " + api.ProjectPath + " " + api.ID + " " + api.Messages[1].ID
			topics = append(topics, distill.Topic{
				Title: "近日切换完成 FUTURE_TITLE", Problem: "确认切换方案", Outcome: "FUTURE_RESULT", Status: "verified",
				Evidence: []string{item.Messages[0].ID, item.Messages[2].ID},
			})
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
				facts[0].Source != 1 || facts[0].Outcome != "重复支付已拦截" ||
				facts[1].Source != 2 || facts[1].Outcome != "重复通知已拦截" ||
				facts[2].Source != 3 || facts[2].Problem != "确认切换方案" || facts[2].Status != "" {
				t.Errorf("scope/order of summary facts is wrong: %s (%v)", input, err)
			}
			for _, private := range []string{"PRIVATE_", "private-", "FUTURE_", "EXCLUDED_", "/home/", "Atlas API", "Relay Worker", "messageId", "sessionId"} {
				if strings.Contains(input, private) {
					t.Errorf("provider request leaked %q: %s", private, input)
				}
			}
			content := `{"sections":[{"category":"outcome","items":[{"title":"重复写入防护","text":"支付与通知重放均已拦截重复写入。","sources":[1,2,1]}]}]}`
			if call == 2 {
				content = strings.Replace(content, "[1,2,1]", "[1,999]", 1)
			} else if call == 3 {
				http.Error(w, "PRIVATE_PROVIDER_ERROR", http.StatusBadGateway)
				return
			} else if call == 4 {
				content = strings.Replace(content, "[1,2,1]", "[3]", 1)
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
	if len(local.Sections) != 2 || local.Sections[1].Title != "期间记录的问题" || len(local.Sections[0].Items) != 1 ||
		!strings.Contains(local.Sections[0].Items[0].Text, "重复通知已拦截") {
		t.Fatalf("local report should merge outcomes across projects: %+v", local)
	}
	model := request(http.MethodPost, path, body)
	wantSources := []string{api.Messages[0].ID, api.Messages[1].ID, queue.Messages[0].ID, queue.Messages[1].ID}
	if model.Mode != "model" || model.Summary != "" || model.Coverage != local.Coverage || model.Sections[0].Title != "主题进展" ||
		!slices.Equal(reportSourceIDs(model), wantSources) ||
		!strings.Contains(model.Markdown, "支付与通知重放均已拦截重复写入") || strings.Contains(model.Markdown, api.ID) {
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
	if result.Mode != "local" || !strings.Contains(result.Notice, "演示工作区") || count != 6 || result.Coverage.Topics <= count || calls.Load() != 0 {
		t.Fatalf("expected a capped daily preview in demo: %+v", result)
	}
}

func TestCustomReportLayoutUsesCalendarDays(t *testing.T) {
	for _, test := range []struct{ from, to, heading string }{
		{"2026-10-31T00:00:00-04:00", "2026-11-03T00:00:00-05:00", "本期成果"},
		{"2026-02-20T00:00:00-05:00", "2026-03-20T00:00:00-04:00", "里程碑与成果"},
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
		if layout.headings[0] != test.heading {
			t.Fatalf("got %q for %s to %s, want %q", layout.headings[0], test.from, test.to, test.heading)
		}
	}
}

func TestLocalReportSourcesSupportDisplayedContent(t *testing.T) {
	fact := func(id, outcome string) reportFact {
		return reportFact{
			topic:   distill.Topic{Title: "回归完成", Outcome: outcome, Status: "verified"},
			sources: []reportSource{{SessionID: "session", MessageID: id}},
		}
	}
	groups := []reportFactGroup{{facts: []reportFact{
		fact("shown", strings.Repeat("验证", 90)),
		fact("omitted", "另一个结果"),
	}}}
	sections, shortened := localReportSections(groups, reportLayouts["daily"])
	if !shortened || !slices.Equal(reportSourceIDs(workReport{Sections: sections}), []string{"shown"}) {
		t.Fatalf("expected only the source for displayed content: %+v", sections)
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
