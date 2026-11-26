package studio

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
	"github.com/xiejun23/Knooo/internal/sessions"
)

func isolateSessionEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "GEMINI_CLI_HOME", "COPILOT_HOME", "QWEN_HOME", "QWEN_RUNTIME_DIR", "WORKBUDDY_CONFIG_DIR", "DSH_HOME"} {
		t.Setenv(name, "")
	}
}

func TestSessionListDiscoversHistoryWithoutRefresh(t *testing.T) {
	isolateSessionEnvironment(t)
	home := t.TempDir()
	dir := filepath.Join(home, ".codex", "sessions")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../sessions/testdata/codex.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(dir, "rollout-sample.jsonl")
	if err := os.WriteFile(transcript, data, 0600); err != nil {
		t.Fatal(err)
	}
	a, err := New(sessions.New(home), "", false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()

	response := httptest.NewRecorder()
	a.routes(http.NotFoundHandler(), "", "").ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/sessions", nil))
	var list struct {
		Sessions []overviewItem
		Coverage sessions.ScanReport
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list.Sessions) != 1 || list.Sessions[0].Count != 2 {
		t.Fatalf("first session list did not discover the transcript: %s", response.Body.String())
	}
	item := list.Sessions[0]
	if item.ID != "codex:codex-fixture" || len(item.Messages) != 0 || item.Platform == "" {
		t.Fatalf("unexpected session list item: %+v", item)
	}
	// Windows can report the same directory using its short or long name.
	for _, paths := range [][2]string{{item.Path, transcript}, {item.SourceRoot, dir}} {
		got, gotErr := os.Stat(paths[0])
		want, wantErr := os.Stat(paths[1])
		if gotErr != nil || wantErr != nil || !os.SameFile(got, want) {
			t.Fatalf("session path %q does not identify %q (stat errors: %v, %v)", paths[0], paths[1], gotErr, wantErr)
		}
	}
	if len(list.Coverage.Environments) != 1 || len(list.Coverage.Roots) == 0 {
		t.Fatalf("missing scan coverage: %+v", list.Coverage)
	}
}

func TestWorkspaceFlow(t *testing.T) {
	a, err := New(nil, t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	const addr = "127.0.0.1:43170"
	const token = "launch-capability"
	handler := a.Handler(addr, token, http.NotFoundHandler(), "1.2.3", "# Release notes", nil, nil)
	request := func(method, path, body, auth, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+addr+path, strings.NewReader(body))
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, path, w.Code, want, w.Body.String())
		}
		return w
	}
	const auth = "Bearer " + token
	request("GET", "/api/sessions", "", "", "", 403)
	request("GET", "/api/sessions", "", auth, "http://localhost:1", 403)
	request("POST", "/api/update", `{"version":"v2.0.0"}`, "", "", 403)
	request("POST", "/api/update", `{"version":"v2.0.0"}`, auth, "http://localhost:1", 403)
	w := request("GET", "/api/about", "", auth, "", 200)
	var about struct {
		Version   string
		Changelog string
		Demo      bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &about); err != nil ||
		about.Version != "1.2.3" || about.Changelog != "# Release notes" || !about.Demo {
		t.Fatalf("expected release metadata in demo workspace, got %s", w.Body.String())
	}
	w = request("GET", "/api/search?q="+url.QueryEscape("幂等键"), "", auth, "", 200)
	var hits []hit
	if err := json.Unmarshal(w.Body.Bytes(), &hits); err != nil || len(hits) != 1 || len(hits[0].Matches) != 2 {
		t.Fatalf("expected one search result with two references, got %s", w.Body.String())
	}
	match := hits[0].Matches[0]
	w = request("GET", "/api/session?id="+url.QueryEscape(match.SessionID), "", auth, "", 200)
	if !strings.Contains(w.Body.String(), match.MessageID) {
		t.Fatalf("missing referenced message %s", match.MessageID)
	}
	w = request("POST", "/api/distill", fmt.Sprintf(`{"id":%q}`, match.SessionID), auth, "", 200)
	var topics []distill.Topic
	if err := json.Unmarshal(w.Body.Bytes(), &topics); err != nil || len(topics) != 1 || topics[0].Status != "observed" {
		t.Fatalf("expected a grounded topic, got %s", w.Body.String())
	}
	body, _ := json.Marshal(note{SessionID: match.SessionID, Topic: topics[0]})
	for range 2 {
		request("POST", "/api/notes", string(body), auth, "", 200)
	}
	w = request("GET", "/api/notes", "", auth, "", 200)
	var notes []note
	if err := json.Unmarshal(w.Body.Bytes(), &notes); err != nil || len(notes) != 1 {
		t.Fatalf("expected one saved note, got %s", w.Body.String())
	}
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	query := url.Values{"kind": {"daily"}, "from": {today.Format(time.RFC3339)}, "to": {today.AddDate(0, 0, 1).Format(time.RFC3339)}}
	w = request("GET", "/api/report?"+query.Encode(), "", auth, "", 200)
	if !strings.Contains(w.Body.String(), "工作日报") || !strings.Contains(w.Body.String(), "Atlas API") {
		t.Fatalf("expected a report, got %s", w.Body.String())
	}
}

func TestProjectFilterDistinguishesWSLDistributions(t *testing.T) {
	const path = "/home/dev/project"
	ubuntu := sessions.Session{
		Platform: "wsl", WSL: "Ubuntu",
		ProjectPath: `\\wsl.localhost\Ubuntu\home\dev\project`, ProjectDisplayPath: path,
	}
	debian := sessions.Session{
		Platform: "wsl", WSL: "Debian",
		ProjectPath: `\\wsl.localhost\Debian\home\dev\project`, ProjectDisplayPath: path,
	}
	if !inProject(ubuntu, path, "wsl:Ubuntu") || inProject(debian, path, "wsl:Ubuntu") {
		t.Fatal("project selection must distinguish the same Linux path across WSL distributions")
	}
	if !inProject(ubuntu, ubuntu.ProjectPath, "wsl:Ubuntu") {
		t.Fatal("project selection must also accept the complete Windows path")
	}
}

func TestReportProjectsMatchMessageDatesAndSource(t *testing.T) {
	a, err := New(nil, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	const start = "2026-11-24T00:00:00+08:00"
	const end = "2026-11-25T00:00:00+08:00"
	const path = "/home/dev/shared"
	a.sessions = []sessions.Session{
		{ID: "ubuntu", Agent: "codex", Project: "Shared", ProjectPath: `\\wsl.localhost\Ubuntu\home\dev\shared`, ProjectDisplayPath: path, Platform: "wsl", WSL: "Ubuntu", Updated: end, Messages: []sessions.Message{{Timestamp: start}}},
		{ID: "duplicate", Project: "Shared", ProjectPath: path, Platform: "wsl", WSL: "Ubuntu", Messages: []sessions.Message{{Timestamp: start}}},
		{ID: "debian", Agent: "claude", Project: "Shared", ProjectPath: path, Platform: "wsl", WSL: "Debian", Messages: []sessions.Message{{Timestamp: start}}},
		{ID: "windows", Agent: "codex", Project: "Next day", ProjectPath: `C:\Next`, Platform: "windows", Messages: []sessions.Message{{Timestamp: end}}},
		{ID: "unknown", Project: "Undated", ProjectPath: `C:\Undated`, Platform: "windows", Updated: start, Messages: []sessions.Message{{Timestamp: "unknown"}}},
		{ID: "archived", Project: "Archived", ProjectPath: `C:\Archive`, Platform: "windows", Messages: []sessions.Message{{Timestamp: start}}},
	}
	reviewTestRequest(t, a, "POST", "/api/history/archive", `{"ids":["archived"],"archived":true}`)
	for _, test := range []struct {
		name, from, to, agent string
		want                  []reportProject
	}{
		{"inclusive start and distinct distributions", start, end, "", []reportProject{{"Shared", path, "wsl", "Ubuntu"}, {"Shared", path, "wsl", "Debian"}}},
		{"agent", start, end, "codex", []reportProject{{"Shared", path, "wsl", "Ubuntu"}}},
		{"next day", end, "2026-11-26T00:00:00+08:00", "", []reportProject{{"Next day", `C:\Next`, "windows", ""}}},
		{"empty", "2026-11-23T00:00:00+08:00", start, "", nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			query := url.Values{"from": {test.from}, "to": {test.to}, "agent": {test.agent}}
			body := reviewTestRequest(t, a, "GET", "/api/report/projects?"+query.Encode(), "")
			var got []reportProject
			if err := json.Unmarshal([]byte(body), &got); err != nil || got == nil || !slices.Equal(got, test.want) {
				t.Fatalf("report projects = %s, want %+v", body, test.want)
			}
		})
	}
}

func TestHistorySearchAndArchive(t *testing.T) {
	data := t.TempDir()
	open := func() *app {
		t.Helper()
		a, err := New(nil, data, false)
		if err != nil {
			t.Fatal(err)
		}
		a.sessions = demoSessions(time.Now())
		return a
	}
	a := open()
	request := func(method, path, body string, status int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		a.routes(http.NotFoundHandler(), "", "").ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != status {
			t.Fatalf("%s %s: status %d, body %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	search := func(agent, query string) map[string]match {
		t.Helper()
		var found map[string]match
		path := "/api/history/search?" + url.Values{"agent": {agent}, "q": {query}}.Encode()
		if err := json.Unmarshal(request("GET", path, "", 200), &found); err != nil {
			t.Fatal(err)
		}
		return found
	}
	if got := search("claude", "单独构造"); len(got) != 1 || got["demo-6"].MessageID != "demo-6-2" {
		t.Fatalf("expected the child transcript reference, got %+v", got)
	}
	if got := search("codex", "单独构造"); len(got) != 0 {
		t.Fatalf("unexpected matches from another agent: %+v", got)
	}
	if got := search("claude", "atlas-api"); len(got) != 2 || got["demo-2"].MessageID != "" {
		t.Fatalf("expected two path-only matches, got %+v", got)
	}
	if got := search("pi", `/.pi/agent/sessions/demo-3.jsonl`); len(got) != 1 || got["demo-3"].SessionID != "demo-3" {
		t.Fatalf("expected a Windows transcript path match, got %+v", got)
	}
	var pathHits []hit
	pathSearch := "/api/search?q=" + url.QueryEscape(`/home/demo/.codex/sessions/demo-1.jsonl`)
	if err := json.Unmarshal(request("GET", pathSearch, "", 200), &pathHits); err != nil ||
		len(pathHits) != 1 || pathHits[0].SessionID != "demo-1" || len(pathHits[0].Matches) != 1 || pathHits[0].Matches[0].MessageID != "" {
		t.Fatalf("expected a WSL file match without message evidence: %+v, %v", pathHits, err)
	}
	const archivePath = "/api/history/archive"
	topicSearch := "/api/search?q=" + url.QueryEscape("幂等键")
	request("POST", archivePath, `{"ids":["demo-1","missing"],"archived":true}`, 409)
	if string(request("GET", topicSearch, "", 200)) == "[]\n" {
		t.Fatal("invalid batch partially archived a session")
	}
	request("POST", archivePath, `{"ids":["demo-1","demo-2"],"archived":true}`, 200)
	a.db.Close()
	a = open()
	defer a.db.Close()

	var list struct{ Sessions []overviewItem }
	if err := json.Unmarshal(request("GET", "/api/sessions", "", 200), &list); err != nil {
		t.Fatal(err)
	}
	if !list.Sessions[0].Archived || !list.Sessions[1].Archived || list.Sessions[5].Archived || list.Sessions[5].ParentID != "demo-2" {
		t.Fatalf("archive state or thread link changed unexpectedly: %+v", list.Sessions)
	}
	if string(request("GET", topicSearch, "", 200)) != "[]\n" {
		t.Fatal("archived history was included in study search")
	}
	request("GET", "/api/session?id=demo-1", "", 200)
	request("POST", archivePath, `{"ids":["demo-1","demo-2"],"archived":false}`, 200)
	if string(request("GET", topicSearch, "", 200)) == "[]\n" {
		t.Fatal("restored history was missing from study search")
	}
}
