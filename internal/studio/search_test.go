package studio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/xiejun23/Knooo/internal/sessions"
)

func TestSearchRefreshesStaleSessionIndex(t *testing.T) {
	isolateSessionEnvironment(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a, err := New(sessions.New(home), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	reviewTestRequest(t, a, http.MethodGet, "/api/sessions", "")
	data, err := os.ReadFile("../sessions/testdata/codex.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "rollout-new.jsonl"), data, 0600); err != nil {
		t.Fatal(err)
	}
	const searchPath = "/api/search?q=codex-fixture&page=1"
	if raw := reviewTestRequest(t, a, http.MethodGet, searchPath, ""); !strings.Contains(raw, `"total":0`) {
		t.Fatalf("search refreshed a recent index: %s", raw)
	}
	a.refresh.Lock()
	a.lastScan = time.Now().Add(-time.Minute)
	a.refresh.Unlock()
	raw := reviewTestRequest(t, a, http.MethodGet, searchPath, "")
	if !strings.Contains(raw, `"total":1`) {
		t.Fatalf("search missed the new transcript: %s", raw)
	}
}

func TestSearchPagesGroupsAndMessages(t *testing.T) {
	a, err := New(nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions = nil
	for i := range 101 {
		id := fmt.Sprintf("session-%03d", i)
		session := sessions.Session{ID: id, Title: id, Agent: "codex"}
		for j := range 7 {
			session.Messages = append(session.Messages, sessions.Message{ID: fmt.Sprintf("%s-%d", id, j), Text: "连接超时，等待重试"})
		}
		a.sessions = append(a.sessions, session)
	}
	found := map[string]bool{}
	for page := 1; page <= 6; page++ {
		raw := reviewTestRequest(t, a, http.MethodGet, fmt.Sprintf("/api/search?q=连接超时&page=%d&agent=codex", page), "")
		var result searchPage
		if err := json.Unmarshal([]byte(raw), &result); err != nil || result.Total != 101 || result.Page != page {
			t.Fatalf("page %d: expected 101 groups, got %s (%v)", page, raw, err)
		}
		for _, hit := range result.Hits {
			if found[hit.SessionID] || len(hit.Matches) != 6 || len(hit.MoreMatches) != 1 {
				t.Fatalf("unexpected search hit: %+v", hit)
			}
			found[hit.SessionID] = true
		}
	}
	if len(found) != 101 {
		t.Fatalf("read %d groups across all pages", len(found))
	}
	raw := reviewTestRequest(t, a, http.MethodGet, "/api/search/matches?q=连接超时&offset=6&session=session-100&agent=codex", "")
	var last searchMatchPage
	if err := json.Unmarshal([]byte(raw), &last); err != nil || last.HasMore || len(last.Matches) != 1 || last.Matches[0].MessageID != "session-100-6" {
		t.Fatalf("last match page = %s (%v)", raw, err)
	}
}

func TestSearchRanksExactTitleAndErrorCode(t *testing.T) {
	a, err := New(nil, "", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	a.sessions = []sessions.Session{
		{ID: "partial", Title: "Log", Messages: []sessions.Message{{ID: "m1", Text: "xECONNRESET_TEST"}}},
		{ID: "exact-code", Title: "Connection issue", Messages: []sessions.Message{{ID: "m2", Text: "Failed: ECONNRESET."}}},
		{ID: "exact-title", Title: "ECONNRESET"},
	}
	raw := reviewTestRequest(t, a, http.MethodGet, "/api/search?q=ECONNRESET&page=1", "")
	var result searchPage
	if err := json.Unmarshal([]byte(raw), &result); err != nil || len(result.Hits) != 3 ||
		result.Hits[0].SessionID != "exact-title" || result.Hits[1].SessionID != "exact-code" || result.Hits[2].SessionID != "partial" {
		t.Fatalf("unexpected rank: %s (%v)", raw, err)
	}
}
