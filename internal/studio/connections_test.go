package studio

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xiejun23/Knooo/internal/distill"
)

type connectionTransport func(*http.Request) (*http.Response, error)

func (transport connectionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestLocalGatewayCatalogAndBudget(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", data)
	dir := filepath.Join(data, "magpie")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"port":3456}`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := magpieDefaultBase(); got != "http://127.0.0.1:3456/v1" {
		t.Fatal(got)
	}
	remote := modelConnection{Preset: "gateway", BaseURL: "http://192.0.2.1:3456/v1"}
	if _, err := remote.validatedConfig(""); err == nil {
		t.Fatal("non-loopback gateway address accepted")
	}
	a, err := New(nil, data, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { a.db.Close() }()
	a.sessions = demoSessions(time.Now())
	session := a.sessions[0]
	var sent atomic.Int32
	var outputLimit atomic.Int32
	outputLimit.Store(512)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "knooo" {
			t.Error("gateway must receive the Knooo user agent")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/models":
			fmt.Fprintf(w, `{"data":[{"id":"vendor/model","owned_by":"vendor","context_window":4096,"max_output_tokens":%d}]}`, outputLimit.Load())
		case "POST /v1/chat/completions":
			sent.Add(1)
			var body struct {
				Model     string `json:"model"`
				MaxTokens int    `json:"max_tokens"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "vendor/model" || body.MaxTokens != 512 {
				t.Errorf("unexpected gateway request: %+v (%v)", body, err)
			}
			topics := fmt.Sprintf(`{"topics":[{"title":"网关主题","status":"observed","evidence":[%q]}]}`, session.Messages[0].ID)
			fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, topics)
		default:
			t.Errorf("unexpected route: %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer gateway.Close()

	handler := a.routes(http.NotFoundHandler(), "", "")
	post := func(path string, body any, status int) []byte {
		t.Helper()
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(string(data))))
		if w.Code != status {
			t.Fatalf("%s: got %d, want %d: %s", path, w.Code, status, w.Body.String())
		}
		return w.Body.Bytes()
	}
	profile := modelConnection{ID: "AAAAAAAAAAAAAAAAAAAAAAAAAA", Preset: "magpie", Name: "本机网关", BaseURL: gateway.URL, Model: "vendor/model"}
	legacy, _ := json.Marshal(profile)
	if _, err := a.db.Exec(`INSERT INTO settings VALUES(?,?)`, "model:"+profile.ID, legacy); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/api/connections", nil))
	var listing struct {
		Profiles []modelConnection `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil || len(listing.Profiles) != 1 ||
		listing.Profiles[0].Preset != "gateway" || listing.Profiles[0].Icon != "magpie" || listing.Profiles[0].GatewayAPI != "chat" {
		t.Fatalf("legacy Magpie settings were not migrated on read: %s", w.Body.String())
	}
	profile = listing.Profiles[0]
	profile.Icon = "gemini"
	encoded := post("/api/connections", profile, 200)
	if err := json.Unmarshal(encoded, &profile); err != nil || !profile.Ready || profile.BaseURL != gateway.URL+"/v1" || profile.Icon != "gemini" {
		t.Fatalf("could not save keyless gateway profile and icon: %s", encoded)
	}
	models := post("/api/models", map[string]string{"id": profile.ID}, 200)
	if !strings.Contains(string(models), `"context_window":4096`) {
		t.Fatalf("catalog lost model limits: %s", models)
	}
	body := map[string]any{"id": session.ID, "connectionId": profile.ID, "preview": true}
	var plan distill.Plan
	encoded = post("/api/distill", body, 200)
	if err := json.Unmarshal(encoded, &plan); err != nil || plan.OutputBudget != 512 || plan.InputBudget != 3584 || sent.Load() != 0 {
		t.Fatalf("unexpected preview: %s (%v)", encoded, err)
	}
	body["preview"], body["confirmation"] = false, plan.Confirmation
	outputLimit.Store(1024)
	post("/api/distill", body, 409)
	if sent.Load() != 0 {
		t.Fatal("stale preview was sent")
	}
	outputLimit.Store(512)
	topics := post("/api/distill", body, 200)
	if !strings.Contains(string(topics), "网关主题") || sent.Load() != 1 {
		t.Fatalf("unexpected result: %s", topics)
	}
}

func TestResponsesGatewayWithOptionalCatalog(t *testing.T) {
	a, err := New(nil, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	a.sessions = demoSessions(time.Now())
	session := a.sessions[0]
	var catalog atomic.Bool
	catalog.Store(true)
	var calls atomic.Int32
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing gateway key")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/models":
			if catalog.Load() {
				fmt.Fprint(w, `{"models":[{"slug":"codex/mock","display_name":"Mock Codex","context_window":8192}]}`)
			} else {
				w.WriteHeader(404)
			}
		case "POST /v1/responses":
			calls.Add(1)
			var body struct {
				Model string `json:"model"`
				Max   int    `json:"max_output_tokens"`
				Input []struct {
					Content string `json:"content"`
				} `json:"input"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Model != "codex/mock" || body.Max != 256 || len(body.Input) != 1 {
				t.Errorf("unexpected Responses request: %+v (%v)", body, err)
			}
			topics := fmt.Sprintf(`{"topics":[{"title":"Responses 网关","status":"observed","evidence":[%q]}]}`, session.Messages[0].ID)
			fmt.Fprintf(w, `{"status":"completed","output":[{"type":"reasoning"},{"type":"message","content":[{"type":"output_text","text":%q}]}]}`, topics)
		default:
			t.Errorf("unexpected gateway route: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer gateway.Close()
	handler := a.routes(http.NotFoundHandler(), "", "")
	post := func(path string, payload any, status int) []byte {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(raw))))
		if w.Code != status {
			t.Fatalf("%s: got %d, want %d: %s", path, w.Code, status, w.Body.String())
		}
		return w.Body.Bytes()
	}
	draft := map[string]any{
		"preset": "gateway", "name": "CC Switch", "gatewayApi": "responses", "icon": "qwen",
		"baseUrl": strings.Replace(gateway.URL, "127.0.0.1", "localhost", 1) + "/v1/responses/",
		"model":   "codex/mock", "apiKey": "test-key", "inputBudget": 4096, "outputBudget": 256,
	}
	if raw := post("/api/models", draft, 200); !strings.Contains(string(raw), `"id":"codex/mock"`) || !strings.Contains(string(raw), `"context_window":8192`) {
		t.Fatalf("could not read Codex catalog: %s", raw)
	}
	var profile modelConnection
	if raw := post("/api/connections", draft, 200); json.Unmarshal(raw, &profile) != nil || profile.BaseURL != gateway.URL+"/v1" || strings.Contains(string(raw), "test-key") {
		t.Fatalf("unexpected saved profile: %s", raw)
	}
	catalog.Store(false)
	post("/api/models", map[string]any{"id": profile.ID, "test": true}, 200)
	body := map[string]any{"id": session.ID, "connectionId": profile.ID, "preview": true}
	var plan distill.Plan
	if raw := post("/api/distill", body, 200); json.Unmarshal(raw, &plan) != nil || plan.InputBudget != 4096 || plan.OutputBudget != 256 || calls.Load() != 1 {
		t.Fatalf("unexpected preview without a catalog: %s", raw)
	}
	body["preview"], body["confirmation"] = false, plan.Confirmation
	if raw := post("/api/distill", body, 200); !strings.Contains(string(raw), "Responses 网关") || calls.Load() != 2 {
		t.Fatalf("unexpected Responses result: %s", raw)
	}
	draft["icon"] = "https://example.com/logo.svg"
	post("/api/connections", draft, 400)
}

func TestModelConnectionFlow(t *testing.T) {
	data := t.TempDir()
	a, err := New(nil, data, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.db.Close() })
	a.sessions = demoSessions(time.Now())
	session := a.sessions[0]
	handler := a.routes(http.NotFoundHandler(), "", "")
	request := func(method, target string, body any, want int) *httptest.ResponseRecorder {
		t.Helper()
		var input io.Reader
		if body != nil {
			encoded, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			input = strings.NewReader(string(encoded))
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(method, target, input))
		if w.Code != want {
			t.Fatalf("%s %s: got %d, want %d: %s", method, target, w.Code, want, w.Body.String())
		}
		return w
	}
	const secret = "fake-api-key-with-private-marker"
	var getRequests, postRequests, testRequests int
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	http.DefaultTransport = connectionTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == "POST" && r.URL.String() == "https://provider.example/custom/v1/messages" {
			if r.Header.Get("x-api-key") != secret || r.Header.Get("Authorization") != "" {
				t.Fatal("edited connection must authenticate with the selected protocol")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"stop_reason":"end_turn","content":[{"type":"text","text":"OK"}]}`))}, nil
		}
		if r.Header.Get("Authorization") == "Bearer wrong-key" {
			return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader(secret))}, nil
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+secret {
			t.Fatalf("provider received unexpected authorization: %q", got)
		}
		var reply string
		switch r.Method + " " + r.URL.String() {
		case "GET https://provider.example/v1/models":
			getRequests++
			if r.Body != nil {
				t.Fatal("models request carried a body")
			}
			reply = `{"data":[{"id":"default-model"},{"id":"chosen-model"},{"id":"chosen-model"}]}`
		case "GET https://provider.example/gateway/v1/models":
			return &http.Response{StatusCode: 404, Body: io.NopCloser(strings.NewReader(secret))}, nil
		case "POST https://provider.example/gateway/v1/chat/completions":
			testRequests++
			var input struct {
				Model     string                     `json:"model"`
				MaxTokens int                        `json:"max_tokens"`
				Messages  []struct{ Content string } `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Model != "manual-model" ||
				input.MaxTokens != 256 || len(input.Messages) != 2 || input.Messages[1].Content != "Connection test" {
				t.Fatalf("unexpected model test payload: %+v (%v)", input, err)
			}
			reply = `{"choices":[{"finish_reason":"stop","message":{"content":"OK"}}]}`
		case "POST https://provider.example/v1/chat/completions":
			postRequests++
			var input struct {
				Model    string                     `json:"model"`
				Messages []struct{ Content string } `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Model != "chosen-model" || len(input.Messages) != 2 {
				t.Fatalf("unexpected completion request: %+v (%v)", input, err)
			}
			var messages []struct{ ID string }
			if err := json.Unmarshal([]byte(input.Messages[1].Content), &messages); err != nil || len(messages) != len(session.Messages) || messages[0].ID != session.Messages[0].ID {
				t.Fatalf("completion should contain only selected session messages: %+v (%v)", messages, err)
			}
			topics := fmt.Sprintf(`{"topics":[{"title":"来自模拟模型的主题","status":"observed","evidence":[%q]}]}`, messages[0].ID)
			reply = fmt.Sprintf(`{"choices":[{"finish_reason":"stop","message":{"content":%q}}]}`, topics)
		default:
			t.Fatalf("unexpected provider request: %s %s", r.Method, r.URL)
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(reply))}, nil
	})
	w := request("POST", "/api/models", map[string]any{"preset": "compatible", "baseUrl": "https://provider.example/v1/models/", "apiKey": secret, "test": true}, 200)
	if w.Body.String() != "[{\"id\":\"chosen-model\"},{\"id\":\"default-model\"}]\n" || getRequests != 1 || testRequests != 0 {
		t.Fatalf("unexpected draft model listing: %s", w.Body)
	}
	w = request("POST", "/api/models", map[string]string{"preset": "compatible", "baseUrl": "https://provider.example/v1", "apiKey": "wrong-key"}, 422)
	if !strings.Contains(w.Body.String(), "HTTP 401") || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("unexpected authentication diagnostic: %s", w.Body)
	}
	gateway := map[string]any{"preset": "compatible", "baseUrl": "https://provider.example/gateway/v1/chat/completions", "apiKey": secret}
	w = request("POST", "/api/models", gateway, 422)
	if !strings.Contains(w.Body.String(), "HTTP 404") || strings.Contains(w.Body.String(), secret) {
		t.Fatalf("unexpected catalog diagnostic: %s", w.Body)
	}
	gateway["model"], gateway["test"] = "manual-model", true
	request("POST", "/api/models", gateway, 200)
	if testRequests != 1 || !strings.Contains(request("GET", "/api/connections", nil, 200).Body.String(), `"profiles":[]`) {
		t.Fatal("testing a model must not save the draft")
	}
	profile := modelConnection{Preset: "compatible", Name: "Test Cloud", BaseURL: "https://provider.example/v1/chat/completions/", Model: "default-model"}
	w = request("POST", "/api/connections", struct {
		modelConnection
		APIKey string `json:"apiKey"`
	}{profile, secret}, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil || !profile.Ready || profile.ID == "" || profile.BaseURL != "https://provider.example/v1" {
		t.Fatalf("connection wasn't saved with a ready key: %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), secret) {
		t.Fatal("save response contains API key")
	}
	var stored string
	if err := a.db.QueryRow("SELECT content FROM settings WHERE name=?", "model:"+profile.ID).Scan(&stored); err != nil || strings.Contains(stored, secret) {
		t.Fatalf("settings must omit the API key: %v", err)
	}
	w = request("POST", "/api/models", map[string]string{"id": profile.ID}, 200)
	if w.Body.String() != "[{\"id\":\"chosen-model\"},{\"id\":\"default-model\"}]\n" || getRequests != 2 || postRequests != 0 {
		t.Fatalf("unexpected stored model listing: %s (GET %d POST %d)", w.Body, getRequests, postRequests)
	}
	distillBody := map[string]any{"id": session.ID, "connectionId": profile.ID, "model": "chosen-model"}
	request("POST", "/api/distill", distillBody, 409)
	distillBody["preview"] = true
	w = request("POST", "/api/distill", distillBody, 200)
	var plan struct {
		Confirmation string
		Included     int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &plan); err != nil || plan.Confirmation == "" || plan.Included != len(session.Messages) || postRequests != 0 {
		t.Fatalf("preview must describe the plan without a provider request: %s", w.Body)
	}
	distillBody["preview"], distillBody["confirmation"] = false, plan.Confirmation
	w = request("POST", "/api/distill", distillBody, 200)
	if !strings.Contains(w.Body.String(), "来自模拟模型的主题") || postRequests != 1 {
		t.Fatalf("expected a topic from the selected model: %s", w.Body)
	}
	w = request("GET", "/api/session?id="+session.ID, nil, 200)
	if !strings.Contains(w.Body.String(), "来自模拟模型的主题") {
		t.Fatal("generated cloud topic should be stored and associated with the session")
	}
	reopened, err := New(nil, data, false)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.db.Close()
	if config, err := reopened.connectionConfig(profile.ID); err != nil || config.APIKey != secret {
		t.Fatalf("a restart should retain the profile and its saved key: %v", err)
	}
	profile.Disabled = true
	w = request("POST", "/api/connections", profile, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil || !profile.Disabled || profile.Ready || !profile.HasKey {
		t.Fatalf("disabling a provider should keep its key while marking it not ready: %s", w.Body.String())
	}
	if _, err := a.connectionConfig(profile.ID); err == nil {
		t.Fatal("disabled provider must not be usable for model calls")
	}
	profile.Disabled = false
	profile.ReasoningEffort = "low"
	profile.Icon = "minimax"
	w = request("POST", "/api/connections", profile, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil || profile.Disabled || !profile.Ready || !profile.HasKey || profile.ReasoningEffort != "low" || profile.Icon != "minimax" {
		t.Fatalf("re-enabling a provider with reasoning effort and minimax icon failed: %s", w.Body.String())
	}
	if config, err := a.connectionConfig(profile.ID); err != nil || config.APIKey != secret || config.ReasoningEffort != "low" {
		t.Fatalf("re-enabled provider lost its saved key or reasoning effort: %+v (%v)", config, err)
	}
	badEffort := profile
	badEffort.ReasoningEffort = "ultra"
	request("POST", "/api/connections", badEffort, 400)
	profile.BaseURL = "https://changed.example/v1"
	request("POST", "/api/models", profile, 400)
	request("POST", "/api/connections", profile, 200)
	request("POST", "/api/models", map[string]string{"id": profile.ID}, 400)
	request("POST", "/api/connections", map[string]any{"id": profile.ID, "remove": true}, 200)
	request("POST", "/api/models", map[string]string{"id": profile.ID}, 400)
	// A supplier preset can keep its identity while using a different endpoint and protocol.
	edited := map[string]any{"preset": "openai", "name": "Custom endpoint", "baseUrl": "https://provider.example/custom/v1/messages", "apiProtocol": "anthropic", "model": "manual-model", "apiKey": secret, "test": true}
	request("POST", "/api/models", edited, 200)
	delete(edited, "test")
	w = request("POST", "/api/connections", edited, 200)
	if err := json.Unmarshal(w.Body.Bytes(), &profile); err != nil || profile.Preset != "openai" || profile.BaseURL != "https://provider.example/custom/v1" || profile.APIProtocol != "anthropic" {
		t.Fatalf("edited endpoint or protocol was lost: %s (%v)", w.Body, err)
	}
	request("POST", "/api/models", map[string]any{"id": profile.ID, "test": true}, 200)
	profile.APIProtocol = "responses"
	request("POST", "/api/models", profile, 400)
	profile.BaseURL = "http://provider.example/custom/v1"
	request("POST", "/api/connections", profile, 400)
}
