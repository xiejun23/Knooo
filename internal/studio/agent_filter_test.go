package studio

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestAgentFilterEndpoints(t *testing.T) {
	const timestamp = "2026-04-03T08:00:00Z"
	a := openReviewTestApp(t, t.TempDir(), sessions.Session{})
	a.sessions = []sessions.Session{
		{ID: "codex", Agent: "codex", ProjectPath: "/shared", Platform: "wsl", WSL: "Ubuntu"},
		{ID: "claude", Agent: "claude", ProjectPath: "/shared", Platform: "wsl", WSL: "Ubuntu"},
		{ID: "other-project", Agent: "codex", ProjectPath: "/other", Platform: "wsl", WSL: "Ubuntu"},
		{ID: "other-location", Agent: "claude", ProjectPath: "/shared", Platform: "wsl", WSL: "Debian"},
		{ID: "app", Agent: "antigravity", AgentVariant: "app", ProjectPath: "/app", Platform: "windows"},
		{ID: "cli", Agent: "antigravity", AgentVariant: "cli", ProjectPath: "/cli", Platform: "windows"},
	}
	for i := range a.sessions {
		item := &a.sessions[i]
		item.Updated = timestamp
		item.Messages = []sessions.Message{{ID: "request", Role: "user", Text: "filter fixture", Timestamp: timestamp}}
		insertLegacyReview(t, a, *item, []distill.Topic{{
			Title: item.ID, Problem: "Investigate " + item.ID, Status: "observed", Evidence: []string{"request"},
		}})
	}
	a.revisions = reviewRevisions(a.sessions)

	request := func(t *testing.T, method, path, body string, result any) {
		t.Helper()
		raw := reviewTestRequest(t, a, method, path, body)
		if err := json.Unmarshal([]byte(raw), result); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	requireIDs := func(t *testing.T, got, want []string) {
		t.Helper()
		slices.Sort(got)
		if !slices.Equal(got, want) {
			t.Fatalf("results = %v, want %v", got, want)
		}
	}
	for _, tc := range []struct {
		name   string
		agents []string
		want   []string
	}{
		{"multiple", []string{"codex", "claude"}, []string{"claude", "codex", "other-location", "other-project"}},
		{"single", []string{"codex"}, []string{"codex", "other-project"}},
		{"variant with family", []string{"antigravity:cli", "codex"}, []string{"cli", "codex", "other-project"}},
		{"family includes variants", []string{"antigravity"}, []string{"app", "cli"}},
		{"missing", nil, []string{"app", "claude", "cli", "codex", "other-location", "other-project"}},
		{"single empty", []string{""}, []string{"app", "claude", "cli", "codex", "other-location", "other-project"}},
		{"empty with known", []string{"", "claude"}, []string{"claude", "other-location"}},
		{"unknown only", []string{"unknown", "antigravity:missing"}, nil},
		{"empty with unknown", []string{"", "unknown", "antigravity:missing"}, nil},
	} {
		t.Run("history/"+tc.name, func(t *testing.T) {
			query := url.Values{"q": {"filter fixture"}, "agent": tc.agents}
			var found map[string]match
			request(t, http.MethodGet, "/api/history/search?"+query.Encode(), "", &found)
			var ids []string
			for id := range found {
				ids = append(ids, id)
			}
			requireIDs(t, ids, tc.want)
		})
	}

	const agents = "&agent=codex&agent=claude"
	const scope = agents + "&project=/shared&location=wsl:Ubuntu"
	wantScoped := []string{"claude", "codex"}
	t.Run("global search", func(t *testing.T) {
		var hits []hit
		request(t, http.MethodGet, "/api/search?q=filter+fixture"+scope, "", &hits)
		var ids []string
		for _, hit := range hits {
			ids = append(ids, hit.SessionID)
		}
		requireIDs(t, ids, wantScoped)
	})
	t.Run("reviews", func(t *testing.T) {
		var response struct{ Entries []reviewEntry }
		request(t, http.MethodGet, "/api/reviews?"+reviewTestWindow+scope, "", &response)
		var ids []string
		for _, entry := range response.Entries {
			ids = append(ids, entry.Session.ID)
		}
		requireIDs(t, ids, wantScoped)
	})
	t.Run("report projects", func(t *testing.T) {
		var projects []reportProject
		request(t, http.MethodGet, "/api/report/projects?"+reviewTestWindow+agents, "", &projects)
		var paths []string
		for _, project := range projects {
			paths = append(paths, project.ProjectPath+"@"+project.WSL)
		}
		requireIDs(t, paths, []string{"/other@Ubuntu", "/shared@Debian", "/shared@Ubuntu"})
	})
	t.Run("report generation", func(t *testing.T) {
		var report workReport
		request(t, http.MethodPost, "/api/report?"+reviewTestWindow+scope, `{"connectionId":""}`, &report)
		var ids []string
		for _, section := range report.Sections {
			for _, item := range section.Items {
				for _, source := range item.Sources {
					ids = append(ids, source.SessionID)
				}
			}
		}
		requireIDs(t, ids, wantScoped)
	})
}
