package studio

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
)

func TestModelConnectionRenameKeepsCiphertext(t *testing.T) {
	a, err := New(nil, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	raw := reviewTestRequest(t, a, http.MethodPost, "/api/connections", `{"preset":"openai","name":"Fixture","apiKey":"fixture-key"}`)
	var profile modelConnection
	if err := json.Unmarshal([]byte(raw), &profile); err != nil || profile.ID == "" {
		t.Fatalf("create connection: %s (%v)", raw, err)
	}
	ciphertext := func() []byte {
		var secret []byte
		if err := a.db.QueryRow(`SELECT secret FROM model_keys WHERE id=?`, profile.ID).Scan(&secret); err != nil {
			t.Fatal(err)
		}
		return secret
	}
	before := ciphertext()
	reviewTestRequest(t, a, http.MethodPost, "/api/connections", `{"preset":"openai","name":"Renamed","id":"`+profile.ID+`"}`)
	if after := ciphertext(); !bytes.Equal(before, after) {
		t.Fatal("metadata-only edit changed the encrypted credential")
	}
	if config, err := a.connectionConfig(profile.ID); err != nil || config.APIKey != "fixture-key" {
		t.Fatalf("renamed connection no longer resolves its saved credential: %v", err)
	}
}

func TestModelLogURLUsesOrigin(t *testing.T) {
	if got := safeLogURL("https://user@example.test:1234/path?query=value#fragment"); got != "https://example.test:1234" {
		t.Fatalf("expected only scheme and host, got %q", got)
	}
	if got := safeLogURL("invalid address"); got != "" {
		t.Fatalf("invalid address was included in logs: %q", got)
	}
}
