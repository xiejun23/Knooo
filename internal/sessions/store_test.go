package sessions

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/klauspost/compress/zstd"
)

func isolatedStore(t *testing.T) (*Store, string) {
	t.Helper()
	for _, name := range []string{"CODEX_HOME", "CLAUDE_CONFIG_DIR", "PI_CODING_AGENT_DIR", "PI_CODING_AGENT_SESSION_DIR", "GEMINI_CLI_HOME", "COPILOT_HOME", "QWEN_HOME", "QWEN_RUNTIME_DIR", "WORKBUDDY_CONFIG_DIR", "DSH_HOME"} {
		t.Setenv(name, "")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(home), home
}

func TestScanPreservesCodexTextAfterContext(t *testing.T) {
	store, home := isolatedStore(t)
	rows := []string{
		`{"type":"session_meta","payload":{"id":"mixed-context","timestamp":"2026-01-01T10:00:00Z"}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T10:00:01Z","payload":{"type":"user_message","message":"<environment_context>fixture</environment_context>"}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T10:00:02Z","payload":{"type":"user_message","message":"<environment_context>fixture</environment_context>\n<user_instructions>fixture</user_instructions>\n请检查导入规则。"}}`,
		`{"type":"response_item","timestamp":"2026-01-01T10:00:03Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md 迁移方案\n请核对标题。"}]}}`,
		`{"type":"response_item","timestamp":"2026-01-01T10:00:04Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/demo\n<INSTRUCTIONS>fixture</INSTRUCTIONS>\n请保留这个请求。"}]}}`,
		`{"type":"event_msg","timestamp":"2026-01-01T10:00:05Z","payload":{"type":"user_message","message":"<environment_context>请解释这段未闭合的标签。"}}`,
	}
	putFile(t, filepath.Join(home, ".codex", "sessions", "rollout-mixed-context.jsonl"), []byte(strings.Join(rows, "\n")))
	items := scanClean(t, store, nil)
	if len(items) != 1 {
		t.Fatalf("got %d sessions, want 1", len(items))
	}
	requireMessages(t, items[0], []int{3, 4, 5, 6},
		"请检查导入规则。", "# AGENTS.md 迁移方案\n请核对标题。", "请保留这个请求。", "<environment_context>请解释这段未闭合的标签。")
}

func TestScanReusesMessageRevisions(t *testing.T) {
	store, home := isolatedStore(t)
	path := filepath.Join(home, ".codex", "sessions", "rollout-revisions.jsonl")
	putFile(t, path, []byte(`{"type":"session_meta","payload":{"id":"revisions"}}`+"\n"+
		`{"type":"event_msg","payload":{"type":"user_message","message":"First turn."}}`+"\n"))
	const id = "codex:revisions"
	first, _, report := store.Scan(nil)
	if len(first) != 1 || report.MessageRevisions[id] != MessageRevision(first[0].Messages) {
		t.Fatal("first scan returned an invalid revision")
	}
	revision := report.MessageRevisions[id]
	report.MessageRevisions[id] = "caller edit"
	first[0].Messages[0].Text = "caller edit"

	second, _, report := store.Scan(nil)
	if len(second) != 1 || report.MessageRevisions[id] != revision ||
		report.MessageRevisionTime != 0 || second[0].Messages[0].Text != "First turn." {
		t.Fatal("warm scan did not reuse the cached revision and messages")
	}

	appendFile(t, path, `{"type":"event_msg","payload":{"type":"agent_message","message":"Second turn."}}`+"\n")
	updated, _, report := store.Scan(nil)
	if len(updated) != 1 || report.MessageRevisions[id] == revision ||
		report.MessageRevisions[id] != MessageRevision(updated[0].Messages) {
		t.Fatal("appended message did not update the revision")
	}
}

func TestDiscoverWorkBuddyAndDeepSeekHarness(t *testing.T) {
	store, home := isolatedStore(t)
	project := filepath.Join(home, "app")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	workbuddy := filepath.Join(home, ".workbuddy-ai", "projects", "app", "wb-main.jsonl")
	putFile(t, workbuddy, []byte(`{"type":"session-meta","sessionId":"wb-main","timestamp":1790503200000}
{"type":"message","role":"user","sessionId":"wb-main","id":"question","content":[{"type":"input_text","text":"@scene:chat"},{"type":"input_text","text":"Check WorkBuddy sources"}]}
{"type":"message","role":"assistant","id":"answer","content":[{"type":"output_text","text":"WorkBuddy response"}]}
{"type":"custom-title","customTitle":"Original WorkBuddy title"}
`))
	meta, err := json.Marshal(map[string]string{"cwd": project})
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, strings.TrimSuffix(workbuddy, ".jsonl")+".meta.json", meta)
	putFile(t, filepath.Join(filepath.Dir(workbuddy), "wb-main", "subagents", "child.jsonl"),
		[]byte(`{"type":"message","role":"assistant","sessionId":"wb-main","content":"Child response"}`))
	putFile(t, filepath.Join(home, ".workbuddy", "projects", "app", "diagnostic.jsonl"),
		[]byte(`{"type":"function_call","sessionId":"debug-only"}`))

	deepseekDir := filepath.Join(home, ".dsh", "sessions", "app", "dsh-main")
	old := filepath.Join(deepseekDir, "session.jsonl")
	putFile(t, old, []byte(`{"type":"session","id":"stale-generation"}`))
	header, err := json.Marshal(map[string]any{"type": "session", "id": "dsh-main", "cwd": project, "createdAt": 1790503200000})
	if err != nil {
		t.Fatal(err)
	}
	compressed := filepath.Join(deepseekDir, "session.v4.jsonl.zstd")
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	first := append(header, '\n')
	second := []byte(`{"type":"user/message","seq":0,"time":1790503200100,"data":{"source":{"kind":"runtime-context"},"content":"Ignore runtime context"}}
{"type":"user/message","seq":1,"time":1790503201000,"data":{"id":"question","source":{"kind":"user"},"content":[{"type":"text","text":"Check Harness sources"}]}}
{"type":"assistant/message","seq":2,"time":1790503202000,"data":{"message":{"id":"answer","content":[{"type":"reasoning","text":"Ignore reasoning"},{"type":"text","text":"Harness response"}]}}}
{"type":"session/title","seq":3,"time":1790503203000,"data":{"title":"Original Harness title"}}
`)
	putFile(t, compressed, encoder.EncodeAll(second, encoder.EncodeAll(first, nil)))
	putFile(t, filepath.Join(filepath.Dir(deepseekDir), "dsh-child", "session.v4.jsonl"),
		[]byte(`{"type":"session","id":"dsh-child","parentSession":"dsh-main","delegationDepth":1}`))
	putFile(t, filepath.Join(filepath.Dir(deepseekDir), "unrecognized", "session.jsonl"),
		[]byte(`{"type":"user/message","data":{"source":{"kind":"user"},"content":"No valid session header"}}`))

	got := make(map[string]Session)
	for _, item := range scanClean(t, store, nil) {
		got[item.ID] = item
	}
	if len(got) != 4 {
		t.Fatalf("found %d sessions, want exactly four", len(got))
	}
	for _, test := range []struct {
		id, title, path, question, answer string
		questionLine                      int
	}{
		{"workbuddy:wb-main", "Original WorkBuddy title", workbuddy, "Check WorkBuddy sources", "WorkBuddy response", 2},
		{"dsh:dsh-main", "Original Harness title", compressed, "Check Harness sources", "Harness response", 3},
	} {
		item := got[test.id]
		if item.Title != test.title || item.Path != test.path || item.ProjectPath != project || len(item.Messages) != 2 {
			t.Fatalf("%s: wrong metadata or message count: %+v", test.id, item)
		}
		question, answer := item.Messages[0], item.Messages[1]
		if question.Text != test.question || question.Line != test.questionLine || answer.Text != test.answer ||
			answer.ID != test.id+":message:answer" {
			t.Fatalf("%s: wrong conversation or anchors: %+v", test.id, item.Messages)
		}
	}
	for child, parent := range map[string]string{"workbuddy:wb-main/child": "workbuddy:wb-main", "dsh:dsh-child": "dsh:dsh-main"} {
		if item := got[child]; item.ParentID != parent || item.ProjectPath != project {
			t.Fatalf("%s: missing parent project: %+v", child, item)
		}
	}
}

func TestAutoDiscoverProfilesAndCompressedArchive(t *testing.T) {
	store, home := isolatedStore(t)
	putFile(t, filepath.Join(home, ".claude-profiles", "work", "projects", "sample", "claude.jsonl"),
		fixture(t, "claude", home))
	putFile(t, filepath.Join(home, ".pi", "agent", "settings.json"), []byte(`{"sessionDir":"~/pi-history"}`))
	putFile(t, filepath.Join(home, "pi-history", "pi.jsonl"), fixture(t, "pi", home))

	plain := filepath.Join(home, ".codex-work", "archived_sessions", "rollout-sample.jsonl")
	transcript := fixture(t, "codex", home)
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	putFile(t, plain+".zst", encoder.EncodeAll(transcript, nil))
	encoder.Close()
	putFile(t, plain, transcript)

	var before Session
	for _, item := range scanClean(t, store, nil) {
		if item.Agent == "codex" {
			before = item
		}
	}
	if before.Path != plain || len(before.Messages) != 2 {
		t.Fatalf("plain twin was not preferred: %+v", before)
	}
	if err := os.Remove(plain); err != nil {
		t.Fatal(err)
	}
	items := scanClean(t, store, nil)
	if len(items) != 3 {
		t.Fatalf("got %d sessions, want three automatically discovered agents", len(items))
	}
	for _, item := range items {
		if item.Agent == "codex" && (item.Path != plain+".zst" || item.ID != before.ID || !reflect.DeepEqual(item.Messages, before.Messages)) {
			t.Fatalf("compressed archive changed the source or evidence anchors: %+v", item)
		}
	}
}

func TestCodexSessionNamesFollowProfileAndRefresh(t *testing.T) {
	store, home := isolatedStore(t)
	profile := filepath.Join(home, ".codex-profiles", "personal #1")
	index := filepath.Join(profile, "session_index.jsonl")
	transcript := fixture(t, "codex", home)
	putFile(t, filepath.Join(profile, "archived_sessions", "rollout-fixture.jsonl"), transcript)
	putFile(t, filepath.Join(profile, "sessions", "state-only.jsonl"), bytes.ReplaceAll(transcript, []byte("codex-fixture"), []byte("state-only")))
	putFile(t, filepath.Join(home, ".codex", "sessions", "default-home.jsonl"), bytes.ReplaceAll(transcript, []byte("codex-fixture"), []byte("default-home")))
	putFile(t, index, []byte("{\"id\":\"default-home\",\"thread_name\":\"Other account\"}\n{\"id\":\"codex-fixture\",\"thread_name\":\"原来的会话名\"}\n"))
	db, err := sql.Open("sqlite", filepath.Join(profile, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE threads (id TEXT PRIMARY KEY, name TEXT, title TEXT);
		INSERT INTO threads VALUES ('codex-fixture', 'Stale name', ''), ('state-only', '状态库里的名字', 'First prompt')`)
	closeErr := db.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("write state fixture: %v, close: %v", err, closeErr)
	}

	titles := func() map[string]string {
		got := make(map[string]string)
		for _, item := range scanClean(t, store, nil) {
			got[item.ID] = item.Title
		}
		return got
	}
	want := map[string]string{
		"codex:codex-fixture": "原来的会话名",
		"codex:state-only":    "状态库里的名字",
		"codex:default-home":  "Inspect the sample project.",
	}
	if got := titles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("session titles = %v, want %v", got, want)
	}
	appendFile(t, index, "{malformed row}\n{\"id\":\"codex-fixture\",\"thread_name\":\"原来的会话名 · 更新\"}\n")
	want["codex:codex-fixture"] = "原来的会话名 · 更新"
	if got := titles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("renamed session titles = %v, want %v", got, want)
	}
	appendFile(t, index, "{\"id\":\"codex-fixture\",\"thread_name\":\"\"}\n")
	want["codex:codex-fixture"] = want["codex:default-home"]
	if got := titles(); !reflect.DeepEqual(got, want) {
		t.Fatalf("titles after clearing a name = %v, want %v", got, want)
	}
}

func putFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, path, content string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(content)
	closeErr := file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
}

func fixture(t *testing.T, name, cwd string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	path, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	return bytes.ReplaceAll(data, []byte(`"__FIXTURE_CWD__"`), path)
}

func scanClean(t *testing.T, store *Store, extra map[string][]string) []Session {
	t.Helper()
	items, warnings, _ := store.Scan(extra)
	if len(warnings) != 0 {
		t.Fatalf("Scan warnings = %v", warnings)
	}
	return items
}

func requireMessages(t *testing.T, s Session, lines []int, texts ...string) {
	t.Helper()
	if len(s.Messages) != len(lines) || len(lines) != len(texts) {
		t.Fatalf("%s: got %d messages, want %d", s.ID, len(s.Messages), len(lines))
	}
	for i, message := range s.Messages {
		if message.Line != lines[i] || message.Text != texts[i] || message.ID == "" {
			t.Errorf("%s: message %d = %+v, want line %d with text %q", s.ID, i, message, lines[i], texts[i])
		}
		if _, err := time.Parse(time.RFC3339Nano, message.Timestamp); err != nil {
			t.Errorf("%s: invalid timestamp %q", s.ID, message.Timestamp)
		}
	}
}

func TestScanAgentsAndProject(t *testing.T) {
	store, home := isolatedStore(t)
	project := filepath.Join(home, "sample-worktree")
	cwd := filepath.Join(project, "nested", "src")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	putFile(t, filepath.Join(project, ".git"), []byte("gitdir: ../main/.git/worktrees/sample\n"))
	putFile(t, filepath.Join(home, ".codex", "archived_sessions", "rollout-fixture.jsonl"), fixture(t, "codex", cwd))
	putFile(t, filepath.Join(home, ".claude", "projects", "sample", "claude-fixture.jsonl"), fixture(t, "claude", cwd))
	putFile(t, filepath.Join(home, ".claude", "projects", "sample", "claude-fixture", "subagents", "workflows", "agent-fixture.jsonl"), fixture(t, "claude-subagent", cwd))
	putFile(t, filepath.Join(home, ".pi", "agent", "sessions", "sample", "pi-fixture.jsonl"), fixture(t, "pi", cwd))

	items := scanClean(t, store, nil)
	if len(items) != 4 {
		t.Fatalf("got %d sessions, want 4", len(items))
	}
	if items[0].Agent != "pi" {
		t.Errorf("first session is %s, want newest Pi session", items[0].ID)
	}
	byID := make(map[string]Session)
	for _, item := range items {
		byID[item.ID] = item
		if item.ProjectPath != project || item.Project != "sample-worktree" || item.Cwd != cwd {
			t.Errorf("%s: project = %q (%q), cwd = %q", item.ID, item.Project, item.ProjectPath, item.Cwd)
		}
		for _, stamp := range []string{item.Start, item.Updated} {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				t.Errorf("%s: invalid timestamp %q", item.ID, stamp)
			}
		}
	}
	requireMessages(t, byID["codex:codex-fixture"], []int{3, 6}, "Inspect the sample project.", "The sample builds.")
	claude := byID["claude:claude-fixture"]
	requireMessages(t, claude, []int{1, 2}, "Describe the sample.", "It is a fixture.\nIt uses fake data.")
	if claude.Title != "Fixture title" || claude.Start != "2026-01-01T10:00:00Z" {
		t.Errorf("unexpected Claude title or start: %q, %q", claude.Title, claude.Start)
	}
	child := byID["claude:claude-fixture/agent-fixture"]
	requireMessages(t, child, []int{1, 2}, "Find the synthetic file.", "The synthetic file is present.")
	if child.ParentID != claude.ID {
		t.Errorf("child parent = %q, want %q", child.ParentID, claude.ID)
	}
	pi := byID["pi:pi-fixture"]
	requireMessages(t, pi, []int{2, 4}, "Explain the fixture.", "Active reply.")
	if pi.Title != "Named Pi fixture" {
		t.Errorf("Pi title = %q", pi.Title)
	}
	again := scanClean(t, store, nil)
	if !reflect.DeepEqual(items, again) {
		t.Fatal("repeat scan changed the snapshot")
	}
	items[0].Messages[0].Text = "caller mutation"
	if result := scanClean(t, store, nil); !reflect.DeepEqual(again, result) {
		t.Fatal("caller mutated the cached snapshot")
	}
}

func TestPiChangesActiveBranchOnAppend(t *testing.T) {
	store, home := isolatedStore(t)
	path := filepath.Join(home, ".pi", "agent", "sessions", "fixture.jsonl")
	putFile(t, path, fixture(t, "pi", home))
	before := scanClean(t, store, nil)[0]
	appendFile(t, path, `{"type":"message","id":"new","parentId":"old","timestamp":"2026-01-01T10:00:06Z","message":{"role":"user","content":"Revisit the earlier branch."}}`+"\n")
	after := scanClean(t, store, nil)[0]
	requireMessages(t, after, []int{2, 3, 7}, "Explain the fixture.", "Abandoned reply.", "Revisit the earlier branch.")
	if before.ID != after.ID || before.Messages[0].ID != after.Messages[0].ID {
		t.Fatal("switching Pi branches changed the IDs of shared records")
	}
}

func TestClaudeNestedParentMetadata(t *testing.T) {
	store, home := isolatedStore(t)
	project := filepath.Join(home, ".claude", "projects", "sample")
	children := filepath.Join(project, "claude-fixture", "subagents")
	putFile(t, filepath.Join(project, "claude-fixture.jsonl"), fixture(t, "claude", home))
	putFile(t, filepath.Join(children, "agent-parent.jsonl"), fixture(t, "claude-subagent", home))
	putFile(t, filepath.Join(children, "agent-child.jsonl"), fixture(t, "claude-subagent", home))

	parentOfChild := func() string {
		t.Helper()
		for _, s := range scanClean(t, store, nil) {
			if s.ID == "claude:claude-fixture/agent-child" {
				if s.Cwd != home {
					t.Fatalf("child cwd = %q, want %q", s.Cwd, home)
				}
				return s.ParentID
			}
		}
		t.Fatal("child session is missing")
		return ""
	}
	if got := parentOfChild(); got != "claude:claude-fixture" {
		t.Errorf("before the sidecar arrived, child parent = %q", got)
	}
	putFile(t, filepath.Join(children, "agent-child.meta.json"), []byte(`{"parentAgentId":"parent"}`))
	if got := parentOfChild(); got != "claude:claude-fixture/agent-parent" {
		t.Errorf("after the sidecar arrived, child parent = %q", got)
	}
}

func TestPiParentPathAndLinearHistory(t *testing.T) {
	store, home := isolatedStore(t)
	dir := filepath.Join(home, ".pi", "agent", "sessions")
	parent := filepath.Join(dir, "parent.jsonl")
	putFile(t, parent, fixture(t, "pi", home))
	child := filepath.Join(dir, "linear.jsonl")
	putFile(t, child, []byte(`{"type":"session","id":"linear-fixture","parentSession":"parent.jsonl","timestamp":"2026-01-01T10:00:00Z"}
{"type":"message","timestamp":"2026-01-01T10:00:01Z","message":{"role":"user","content":"Legacy fixture."}}
{"type":"message","message":{"role":"assistant","content":"Legacy reply.","timestamp":1767261602000}}
`))
	items := scanClean(t, store, nil)
	if len(items) != 2 {
		t.Fatalf("got %d sessions, want 2", len(items))
	}
	s := items[1]
	if s.ID != "pi:linear-fixture" || s.ParentID != items[0].ID || s.Cwd != home {
		t.Fatalf("got child ID %q, parent %q, cwd %q", s.ID, s.ParentID, s.Cwd)
	}
	requireMessages(t, s, []int{2, 3}, "Legacy fixture.", "Legacy reply.")
}

func TestConfiguredSourcesAndArchiveMove(t *testing.T) {
	store, home := isolatedStore(t)
	base := filepath.Join(home, "codex-custom")
	t.Setenv("CODEX_HOME", base)
	active := filepath.Join(base, "sessions", "day", "rollout-fixture.jsonl")
	archived := filepath.Join(base, "archived_sessions", "rollout-fixture.jsonl")
	data := fixture(t, "codex", home)
	putFile(t, active, data)
	putFile(t, archived, data)
	custom := filepath.Join(home, "custom-claude")
	putFile(t, filepath.Join(custom, "fixture.jsonl"), fixture(t, "claude", home))
	extra := map[string][]string{"codex": {base, filepath.Dir(active)}, "claude": {custom}}

	before := scanClean(t, store, extra)
	if len(before) != 2 || before[1].Path != active || before[0].ProjectPath != home {
		t.Fatal("expected one configured Claude session and one live Codex copy")
	}
	if err := os.Remove(active); err != nil {
		t.Fatal(err)
	}
	after := scanClean(t, store, extra)
	if len(after) != 2 || after[1].Path != archived || before[1].ID != after[1].ID ||
		!reflect.DeepEqual(before[1].Messages, after[1].Messages) {
		t.Fatal("moving to archived_sessions changed the Codex identity or anchors")
	}
	if got, err := os.ReadFile(archived); err != nil || !bytes.Equal(got, data) {
		t.Fatal("scan modified the archive")
	}
	if items, warns, _ := store.Scan(map[string][]string{"unknown": {home}, "pi": {filepath.Join(home, "missing")}}); len(items) != 1 || len(warns) != 2 {
		t.Fatalf("got %d sessions and %d warnings, want 1 and 2", len(items), len(warns))
	}
}

func TestWSLDiscoveryOutput(t *testing.T) {
	var out []byte
	for _, unit := range utf16.Encode([]rune("\ufeffUbuntu-24.04\r\nDebian\r\n")) {
		out = binary.LittleEndian.AppendUint16(out, unit)
	}
	want := []string{"Ubuntu-24.04", "Debian"}
	for _, input := range [][]byte{out, []byte("Ubuntu-24.04\nDebian\n../bad\n")} {
		if got := wslDistributions(input); !reflect.DeepEqual(got, want) {
			t.Errorf("distributions = %v, want %v", got, want)
		}
	}
	for _, input := range []string{`\\wsl.localhost\Ubuntu-24.04\home\fixture\code`, `//WSL$/Ubuntu-24.04/home/fixture/code`} {
		distro, path := wslPathParts(input)
		if distro != "Ubuntu-24.04" || path != "/home/fixture/code" {
			t.Errorf("wslPathParts(%q) = %q, %q", input, distro, path)
		}
		if platform, distro := locationOf(input); platform != "wsl" || distro != "Ubuntu-24.04" {
			t.Errorf("locationOf(%q) = %q, %q", input, platform, distro)
		}
	}
	if runtime.GOOS == "windows" {
		source := `\\wsl.localhost\Ubuntu-24.04\home\fixture\.codex\sessions\rollout.jsonl`
		for cwd, want := range map[string]string{
			"/home/fixture/code": `\\wsl.localhost\Ubuntu-24.04\home\fixture\code`,
			"/mnt/d/develop":     `D:\develop`,
			`D:\native`:          `D:\native`,
		} {
			if got := projectDirectory(cwd, source); !strings.EqualFold(got, want) {
				t.Errorf("projectDirectory(%q) = %q, want %q", cwd, got, want)
			}
		}
	}
}

func TestScanCoverageMatchesIndexedSessions(t *testing.T) {
	store, home := isolatedStore(t)
	t.Setenv("WSL_DISTRO_NAME", "Fixture-WSL")
	active := filepath.Join(home, ".codex", "sessions")
	archive := filepath.Join(home, ".codex", "archived_sessions")
	path := filepath.Join(active, "rollout-fixture.jsonl")
	putFile(t, path, fixture(t, "codex", home))
	putFile(t, filepath.Join(archive, "rollout-fixture.jsonl"), fixture(t, "codex", home))
	putFile(t, filepath.Join(active, "rollout-broken.jsonl"), []byte("invalid JSON\n"))

	items, warnings, coverage := store.Scan(nil)
	platform, distro := runtime.GOOS, ""
	if platform == "linux" {
		platform, distro = "wsl", "Fixture-WSL"
	}
	if len(items) != 1 || len(warnings) != 1 || items[0].Path != path || items[0].SourceRoot != active ||
		items[0].Platform != platform || items[0].WSL != distro {
		t.Fatalf("unexpected scan result: %+v, warnings: %v", items, warnings)
	}
	roots := map[string]ScanRoot{}
	for _, root := range coverage.Roots {
		roots[root.Directory] = root
	}
	if got := roots[active]; got.State != "partial" || got.Sessions != 1 || got.WSL != distro {
		t.Errorf("active scan root = %+v", got)
	}
	if got := roots[archive]; got.State != "scanned" || got.Sessions != 0 {
		t.Errorf("archive scan root = %+v", got)
	}
	if got := roots[filepath.Join(home, ".claude", "projects")]; got.State != "missing" {
		t.Errorf("missing default scan root = %+v", got)
	}
}

func TestClaudeForkRetainsParent(t *testing.T) {
	store, home := isolatedStore(t)
	dir := filepath.Join(home, ".claude", "projects", "sample")
	putFile(t, filepath.Join(dir, "parent.jsonl"), []byte(`{"type":"user","sessionId":"parent","uuid":"p","message":{"content":"Original"}}`+"\n"))
	putFile(t, filepath.Join(dir, "fork.jsonl"), []byte(`{"type":"user","sessionId":"fork","uuid":"f","forkedFrom":{"sessionId":"parent","messageUuid":"p"},"message":{"content":"Fork"}}`+"\n"))
	putFile(t, filepath.Join(dir, "fork.meta.json"), []byte(`{"parentAgentId":"unrelated"}`))
	for _, session := range scanClean(t, store, nil) {
		if session.ID == "claude:fork" && session.ParentID == "claude:parent" {
			return
		}
	}
	t.Fatal("Claude fork did not retain its original thread parent")
}

func TestAdditionalAgentsAutoDiscovery(t *testing.T) {
	store, home := isolatedStore(t)
	project := filepath.Join(home, "project")
	putFile(t, filepath.Join(project, ".git", "HEAD"), []byte("ref: refs/heads/main\n"))
	gemini := filepath.Join(home, ".gemini", "tmp", "project", "chats", "session-test.jsonl")
	copilot := filepath.Join(home, ".copilot", "session-state", "copilot-fixture", "events.jsonl")
	qwen := filepath.Join(home, ".qwen", "projects", "project", "chats", "qwen-fixture.jsonl")
	for agent, path := range map[string]string{"gemini": gemini, "copilot": copilot, "qwen": qwen} {
		putFile(t, path, fixture(t, agent, project))
	}
	putFile(t, filepath.Join(filepath.Dir(gemini), "..", ".project_root"), []byte(project+"\n"))
	putFile(t, filepath.Join(filepath.Dir(copilot), "debug.jsonl"), []byte("not a session"))
	putFile(t, filepath.Join(filepath.Dir(qwen), "..", "debug.jsonl"), []byte("not a session"))
	legacy := filepath.Join(filepath.Dir(gemini), "session-legacy.json")
	putFile(t, legacy, []byte(`{
  "sessionId":"gemini-legacy",
  "startTime":"2026-11-22T01:00:00Z",
  "lastUpdated":"2026-11-22T01:02:00Z",
  "messages":[
    {"id":"legacy-prompt","type":"user","content":"Legacy Gemini prompt","timestamp":"2026-11-22T01:01:00Z"}
  ]
}`))
	byID := func() map[string]Session {
		items := scanClean(t, store, nil)
		if len(items) != 4 {
			t.Fatalf("indexed %d sessions, want 4", len(items))
		}
		result := make(map[string]Session)
		for _, item := range items {
			if item.ProjectPath != project || item.Platform == "" || item.SourceRoot == "" {
				t.Errorf("incorrect provenance for %s: %q %q %q", item.ID, item.ProjectPath, item.Platform, item.SourceRoot)
			}
			result[item.ID] = item
		}
		return result
	}
	items := byID()
	for id, want := range map[string]struct{ path, title string }{
		"gemini:gemini-fixture":   {gemini, "Gemini pagination"},
		"gemini:gemini-legacy":    {legacy, "Legacy Gemini prompt"},
		"copilot:copilot-fixture": {copilot, "Copilot report timezone"},
		"qwen:qwen-fixture":       {qwen, "Qwen queue retries"},
	} {
		if item := items[id]; item.Title != want.title || item.Path != want.path {
			t.Errorf("%s: got title %q and source %q", id, item.Title, item.Path)
		}
	}
	requireMessages(t, items["gemini:gemini-fixture"], []int{2, 5}, "Inspect pagination", "Added cursor pagination")
	requireMessages(t, items["gemini:gemini-legacy"], []int{6}, "Legacy Gemini prompt")
	requireMessages(t, items["copilot:copilot-fixture"], []int{2, 3}, "Inspect report timezone", "Use UTC for report queries")
	requireMessages(t, items["qwen:qwen-fixture"], []int{1, 2}, "Inspect queue retries", "Use bounded backoff")
	appendFile(t, gemini, "{\"$set\":{\"summary\":\"Renamed Gemini session\",\"messages\":[{\"id\":\"new\",\"type\":\"user\",\"timestamp\":\"2026-11-23T01:05:00Z\",\"content\":\"New checkpoint\"}]}}\n")
	updated := byID()["gemini:gemini-fixture"]
	if updated.Title != "Renamed Gemini session" {
		t.Fatalf("Gemini title after append = %q", updated.Title)
	}
	requireMessages(t, updated, []int{8}, "New checkpoint")
}
