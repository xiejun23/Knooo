package studio

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestDiscoverSourcesMagpie(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home, config := t.TempDir(), t.TempDir()
	writeDiscoveryFixture(t, config, "magpie/providers.json", `{"providers":[
		{"id":"relay","name":"Relay","key":"fixture-magpie-a","chat":"https://relay.example/chat/v1","responses":"https://relay.example/v1/responses","baseAPI":"responses","models":["model-a"],"icon":"openrouter"},
		{"id":"native","key":"fixture-magpie-b","anthropic":"https://anthropic.example","chat":"https://relay.example/v1","keyProtocol":"anthropic","authHeader":"x-api-key"},
		{"id":"gemini","key":"fixture-magpie-c","gemini":"https://gemini.example/v1beta"},
		{"id":"unsupported","key":"fixture-magpie-d","chat":"https://relay.example","baseAPI":"decide"},
		{"id":"off","key":"fixture-magpie-e","chat":"https://relay.example","off":true},
		{"id":"remote-no-key","chat":"https://relay.example"},
		{"id":"bearer-anthropic","key":"fixture-magpie-f","anthropic":"https://relay.example","authHeader":"bearer"},
		{"id":"oauth","key":"sk-ant-oat01-fixture","anthropic":"https://relay.example"},
		{"id":"header-override","key":"fixture-magpie-g","anthropic":"https://relay.example","headers":{"Authorization":"Bearer fixture-token"}}
	]}`)
	writeDiscoveryFixture(t, home, ".config/magpie/providers.json", `{"providers":[
		{"id":"local","chat":"http://localhost:3425"},
		{"id":"other","responses":"http://[::1]:9999/v1/responses","key":"fixture-local"}
	]}`)
	found := discoverSources(home, config)
	want := []modelConnection{
		{Preset: "compatible", APIProtocol: "responses", BaseURL: "https://relay.example/v1", Name: "Relay", Model: "model-a", Icon: "openrouter"},
		{Preset: "compatible", APIProtocol: "anthropic", BaseURL: "https://anthropic.example/v1", Name: "native", Icon: "magpie"},
		{Preset: "compatible", APIProtocol: "gemini", BaseURL: "https://gemini.example/v1beta", Name: "gemini", Icon: "magpie"},
		{Preset: "gateway", GatewayAPI: "chat", BaseURL: "http://127.0.0.1:3425/v1", Name: "local", Icon: "magpie"},
		{Preset: "gateway", GatewayAPI: "responses", BaseURL: "http://[::1]:9999/v1", Name: "other", Icon: "magpie"},
	}
	assertDiscoveryProfiles(t, found, want)
	if found[0].source != "magpie" || found[0].key != "fixture-magpie-a" || !strings.HasSuffix(found[0].ref, "#relay") {
		t.Fatal("missing Magpie discovery metadata")
	}
	if got := discoverSources(home, filepath.Join(home, ".config")); len(got) != 2 {
		t.Fatalf("identical Magpie paths returned %d candidates", len(got))
	}
}

func TestDiscoverSourcesClaudeCode(t *testing.T) {
	home, override := t.TempDir(), t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	writeDiscoveryFixture(t, home, ".claude/settings.json", `{"model":"model-default","env":{"ANTHROPIC_API_KEY":"fixture-claude-a"}}`)
	assertDiscoveryProfiles(t, discoverSources(home, ""), []modelConnection{
		{Preset: "compatible", APIProtocol: "anthropic", BaseURL: "https://api.anthropic.com/v1", Name: "Claude Code", Model: "model-default", Icon: "claude"},
	})
	t.Setenv("CLAUDE_CONFIG_DIR", override)
	writeDiscoveryFixture(t, override, "settings.json", `{"env":{"ANTHROPIC_BASE_URL":"https://relay.example/anthropic","ANTHROPIC_API_KEY":"fixture-claude-b","ANTHROPIC_MODEL":"model-custom"}}`)
	found := discoverSources(home, "")
	assertDiscoveryProfiles(t, found, []modelConnection{
		{Preset: "compatible", APIProtocol: "anthropic", BaseURL: "https://relay.example/anthropic/v1", Name: "Claude Code", Model: "model-custom", Icon: "claude"},
	})
	if found[0].key != "fixture-claude-b" || found[0].source != "claude-code" || found[0].ref != filepath.Join(override, "settings.json") {
		t.Fatal("missing Claude Code discovery metadata")
	}
	for _, content := range []string{
		`{"env":{"ANTHROPIC_BASE_URL":"https://relay.example","ANTHROPIC_AUTH_TOKEN":"fixture-bearer"}}`,
		`{"env":{"ANTHROPIC_API_KEY":"fixture-api-key","ANTHROPIC_AUTH_TOKEN":"fixture-bearer"}}`,
		`{"api":"openai_responses","env":{"ANTHROPIC_BASE_URL":"https://relay.example","ANTHROPIC_AUTH_TOKEN":"fixture-bearer"}}`,
		`{"env":{"ANTHROPIC_API_KEY":"fixture-api-key","CLAUDE_CODE_USE_BEDROCK":"1"}}`,
		`{"apiKeyHelper":"exit 99","env":{"ANTHROPIC_API_KEY":"fixture-api-key"}}`,
		`{"env":{"ANTHROPIC_API_KEY":"${ANTHROPIC_API_KEY}"}}`,
	} {
		writeDiscoveryFixture(t, override, "settings.json", content)
		if got := discoverSources(home, ""); len(got) != 0 {
			t.Fatal("unsupported Claude Code settings returned a candidate")
		}
	}
}

func TestDiscoverSourcesCCSwitch(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := filepath.Join(t.TempDir(), "home #fixtures%")
	path := writeDiscoveryFixture(t, home, ".cc-switch/cc-switch.db", "")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE providers (id TEXT, app_type TEXT, name TEXT, settings_config TEXT, category TEXT, meta TEXT)`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][]any{
		{"anthropic", "claude", "Native", `{"env":{"ANTHROPIC_BASE_URL":"https://relay.example/v1/messages","ANTHROPIC_API_KEY":"fixture-cc-a","ANTHROPIC_DEFAULT_SONNET_MODEL":"sonnet"}}`, "custom", nil},
		{"responses", "claude", "Converted", `{"env":{"ANTHROPIC_BASE_URL":"https://relay.example/v1","ANTHROPIC_AUTH_TOKEN":"fixture-cc-b"}}`, "custom", `{"apiFormat":"openai_responses"}`},
		{"gemini", "gemini", "Gemini", `{"env":{"GOOGLE_GEMINI_BASE_URL":"https://gemini.example/relay","GEMINI_API_KEY":"fixture-cc-c","GEMINI_MODEL":"gemini-model"}}`, nil, `{}`},
		{"opencode", "opencode", "OpenCode", `{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"https://open.example/v1","apiKey":"fixture-cc-d"},"models":{"z":{},"a":{}}}`, nil, nil},
		{"sub", "claude", "Subscription", `{"env":{"ANTHROPIC_API_KEY":"fixture-cc-e"}}`, "subscription", nil},
		{"off", "claude", "Disabled", `{"env":{"ANTHROPIC_API_KEY":"fixture-cc-f"}}`, "custom", `{"enabled":false}`},
		{"token", "claude", "Bearer only", `{"env":{"ANTHROPIC_BASE_URL":"https://relay.example","ANTHROPIC_AUTH_TOKEN":"fixture-cc-g"}}`, "custom", nil},
		{"unknown", "claude", "Unknown", `{"env":{"ANTHROPIC_API_KEY":"fixture-cc-h"}}`, "custom", `{"apiFormat":"unsupported"}`},
		{"oversize", "claude", "Oversize", strings.Repeat(" ", discoveryRowLimit+1), "custom", nil},
	} {
		if _, err := db.Exec(`INSERT INTO providers VALUES (?, ?, ?, ?, ?, ?)`, row...); err != nil {
			t.Fatal(err)
		}
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	found := discoverSources(home, "")
	assertDiscoveryProfiles(t, found, []modelConnection{
		{Preset: "compatible", APIProtocol: "anthropic", BaseURL: "https://relay.example/v1", Name: "Native", Model: "sonnet", Icon: "ccswitch"},
		{Preset: "compatible", APIProtocol: "responses", BaseURL: "https://relay.example/v1", Name: "Converted", Icon: "ccswitch"},
		{Preset: "compatible", APIProtocol: "gemini", BaseURL: "https://gemini.example/relay/v1beta", Name: "Gemini", Model: "gemini-model", Icon: "ccswitch"},
		{Preset: "compatible", APIProtocol: "chat", BaseURL: "https://open.example/v1", Name: "OpenCode", Model: "a", Icon: "ccswitch"},
	})
	if found[1].source != "cc-switch" || found[1].key != "fixture-cc-b" || !strings.HasSuffix(found[1].ref, "#claude/responses") {
		t.Fatal("missing CC Switch discovery metadata")
	}
	if after, err := os.ReadFile(path); err != nil || string(original) != string(after) {
		t.Fatal("discovery changed the source database")
	}
	if entries, err := os.ReadDir(filepath.Dir(path)); err != nil || len(entries) != 1 {
		t.Fatal("discovery created a database sidecar")
	}
	if _, err := db.Exec(`ALTER TABLE providers DROP COLUMN meta`); err != nil {
		t.Fatal(err)
	}
	if got := discoverSources(home, ""); len(got) != 5 {
		t.Fatalf("legacy schema returned %d candidates", len(got))
	}
}

func TestDiscoverSourcesCCSwitchActiveWAL(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	path := writeDiscoveryFixture(t, home, ".cc-switch/cc-switch.db", "")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`PRAGMA journal_mode=WAL`,
		`CREATE TABLE providers (id TEXT, app_type TEXT, name TEXT, settings_config TEXT, category TEXT, meta TEXT)`,
		`INSERT INTO providers VALUES ('fixture','claude','Active','{"env":{"ANTHROPIC_API_KEY":"fixture-wal-key"}}',NULL,NULL)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if got := discoverSources(home, ""); len(got) != 1 || got[0].key != "fixture-wal-key" {
		t.Fatalf("active database returned %d candidates", len(got))
	}
}

func TestDiscoverSourcesLimits(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	home := t.TempDir()
	providers := make([]map[string]string, discoveryEntryLimit+1)
	for i := range providers {
		providers[i] = map[string]string{"id": fmt.Sprint(i), "chat": "http://127.0.0.1:8765/v1"}
	}
	raw, err := json.Marshal(map[string]any{"providers": providers})
	if err != nil {
		t.Fatal(err)
	}
	writeDiscoveryFixture(t, home, ".config/magpie/providers.json", "\xef\xbb\xbf"+string(raw))
	if got := discoverSources(home, ""); len(got) != discoveryEntryLimit {
		t.Fatalf("discovered %d candidates, want %d", len(got), discoveryEntryLimit)
	}
	writeDiscoveryFixture(t, home, ".config/magpie/providers.json", string(raw)+strings.Repeat(" ", discoveryConfigLimit))
	if got := discoverSources(home, ""); len(got) != 0 {
		t.Fatal("oversized config returned candidates")
	}
}

func TestDiscoveryRejectsUnsafeCredentialsAndAddresses(t *testing.T) {
	for _, tc := range []struct{ base, key, protocol string }{
		{"https://relay.example?api_key=fixture-secret", "fixture-secret", "chat"},
		{"https://user:fixture-secret@relay.example", "fixture-secret", "chat"},
		{"https://relay.example/%66ixture-secret/v1", "fixture-secret", "chat"},
		{"https://relay.example/sk-other-credential/v1", "fixture-secret", "chat"},
		{"https://relay.example/token/other-credential", "fixture-secret", "chat"},
		{"https://relay.example/v1#fixture-credential", "fixture-secret", "chat"},
		{"https://relay.example", "$OPENAI_API_KEY", "chat"},
		{"https://relay.example", "OPENAI_API_KEY", "chat"},
		{"https://relay.example", "dummy-key", "chat"},
		{"https://relay.example", "eyJfixture.header.signature", "responses"},
		{"https://chatgpt.com/backend-api/codex", "fixture-secret", "responses"},
		{"http://relay.example", "fixture-secret", "chat"},
		{"http://127.0.0.1:8888", "fixture-secret", "anthropic"},
		{"https://127.0.0.1:8888", "", "chat"},
	} {
		if got := appendDiscoveryConnection(nil, "fixture", "ref", "Fixture", "generic", tc.protocol, tc.base, tc.key, ""); len(got) != 0 {
			t.Errorf("unexpected candidate for fixture URL %q and protocol %q", tc.base, tc.protocol)
		}
	}
}

func writeDiscoveryFixture(t *testing.T, dir, name, data string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertDiscoveryProfiles(t *testing.T, found []sourceConnection, want []modelConnection) {
	t.Helper()
	var profiles []modelConnection
	for _, connection := range found {
		profiles = append(profiles, connection.profile)
	}
	if !reflect.DeepEqual(profiles, want) {
		t.Fatalf("profiles = %+v, want %+v", profiles, want)
	}
}
