package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryImportAndRestart(t *testing.T) {
	home, data := t.TempDir(), t.TempDir()
	for _, name := range []string{"HOME", "USERPROFILE"} {
		t.Setenv(name, home)
	}
	for _, name := range []string{"XDG_CONFIG_HOME", "APPDATA"} {
		t.Setenv(name, filepath.Join(home, ".config"))
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	folder := filepath.Join(home, ".config", "magpie")
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	const key = "sk-private-model-fixture-739"
	writeSource := func(model string) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"providers": []any{map[string]any{
			"id": "fixture", "name": "Fixture", "key": key, "chat": "https://fixture.example/v1", "models": []string{model},
		}}})
		if err := os.WriteFile(filepath.Join(folder, "providers.json"), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeSource("fixture-model")
	open := func() *app {
		t.Helper()
		a, err := New(nil, data, false)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = a.Close() })
		return a
	}
	a := open()
	const discovery = "/api/connections/discovery"
	const connections = "/api/connections"
	request := func(method, path, body string, want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		a.routes(http.NotFoundHandler(), "", "").ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		if w.Code != want || strings.Contains(w.Body.String(), key) {
			t.Fatalf("%s %s: expected status %d and no secret, got %d", method, path, want, w.Code)
		}
		return w.Body.Bytes()
	}
	preview := func() []discoveredConnection {
		var items []discoveredConnection
		if err := json.Unmarshal(request("GET", discovery, "", 200), &items); err != nil {
			t.Fatal(err)
		}
		return items
	}
	items := preview()
	if len(items) != 1 || items[0].Profile.Model != "fixture-model" {
		t.Fatal("expected one discovered model")
	}
	writeSource("updated-model")
	request("POST", discovery, `{"id":"`+items[0].ID+`"}`, 409)
	items = preview()
	if len(items) != 1 || items[0].Profile.Model != "updated-model" {
		t.Fatal("expected updated source")
	}
	importBody := `{"id":"` + items[0].ID + `"}`
	var profile modelConnection
	if err := json.Unmarshal(request("POST", discovery, importBody, 200), &profile); err != nil || !profile.HasKey {
		t.Fatal("expected saved connection with API key")
	}
	request("POST", discovery, importBody, 409)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(folder, "providers.json")); err != nil {
		t.Fatal(err)
	}
	a = open()
	config, err := a.connectionConfig(profile.ID)
	if err != nil || config.APIKey != key || config.Model != "updated-model" {
		t.Fatal("saved model key was not restored")
	}
	var secret string
	if err := a.db.QueryRow(`SELECT hex(secret) FROM model_keys WHERE id=?`, profile.ID).Scan(&secret); err != nil || secret == "" {
		t.Fatal("missing encrypted model key")
	}
	contents, err := os.ReadFile(filepath.Join(data, "studio.sqlite"))
	if err != nil || strings.Contains(string(contents), key) {
		t.Fatal("model key should be encrypted on disk")
	}
	profile.APIProtocol = "responses"
	body, _ := json.Marshal(profile)
	request("POST", connections, string(body), 200)
	if _, err := a.connectionConfig(profile.ID); err == nil {
		t.Fatal("old key reused after protocol change")
	}
	withKey := strings.TrimSuffix(string(body), "}") + `,"apiKey":"` + key + `"}`
	request("POST", connections, withKey, 200)
	request("POST", connections, strings.TrimSuffix(string(body), "}")+`,"clearKey":true}`, 200)
	if _, err := a.connectionConfig(profile.ID); err == nil {
		t.Fatal("cleared key is still usable")
	}
}
