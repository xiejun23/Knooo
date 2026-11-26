package studio

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestLargeReportDraftCanBeSavedAfterRegeneration(t *testing.T) {
	a := openReviewTestApp(t, t.TempDir(), reviewTestSession())
	query, err := url.ParseQuery(reviewTestWindow)
	if err != nil {
		t.Fatal(err)
	}
	from, to, err := reviewWindow(query)
	if err != nil {
		t.Fatal(err)
	}
	scope := reportDocumentScope(query, from, to)
	path := "/api/report/document?" + reviewTestWindow
	draft := workReport{Title: "长报告", Mode: "local", Sections: []reportSection{}, Markdown: strings.Repeat("报告事项\n", 100_000)}
	store := func(report workReport) {
		t.Helper()
		if err := a.storeGeneratedReport(t.Context(), scope, report); err != nil {
			t.Fatal(err)
		}
	}
	save := func(revision int) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"draft": draft, "draftRevision": revision})
		if err != nil {
			t.Fatal(err)
		}
		reviewTestRequest(t, a, http.MethodPost, path, string(body))
	}
	store(draft)
	save(0)
	store(workReport{Title: "新报告", Mode: "local", Sections: []reportSection{}})
	draft.Title = "长报告的编辑稿"
	save(1)
	if got := reviewTestRequest(t, a, http.MethodGet, path, ""); !strings.Contains(got, draft.Title) {
		t.Fatal("the larger saved draft should remain editable after regeneration")
	}
}
