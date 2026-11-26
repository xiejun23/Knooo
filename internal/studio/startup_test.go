package studio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopStartupRequiresLocalCapabilityAndInstalledWorkspace(t *testing.T) {
	const addr = "127.0.0.1:43170"
	const token = "desktop-launch-capability"
	a := &app{demo: true}
	handler := a.Handler(addr, token, http.NotFoundHandler(), "", "", nil)
	request := func(method, body, auth, origin string, want int) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "http://"+addr+"/api/desktop-startup", strings.NewReader(body))
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s desktop startup: got %d, want %d: %s", method, w.Code, want, w.Body)
		}
		return w
	}

	const auth = "Bearer " + token
	request(http.MethodGet, "", "", "", http.StatusForbidden)
	request(http.MethodPost, `{"enabled":true}`, "", "", http.StatusForbidden)
	request(http.MethodPost, `{"enabled":true}`, auth, "http://localhost:1", http.StatusForbidden)
	request(http.MethodPost, `{"enabled":true,"path":"outside.exe"}`, auth, "", http.StatusBadRequest)
	request(http.MethodPost, `{}`, auth, "", http.StatusBadRequest)
	request(http.MethodPost, `{"enabled":true}`, auth, "", http.StatusConflict)

	w := request(http.MethodGet, "", auth, "", http.StatusOK)
	var response struct{ State desktopStartupState }
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.State != startupUnavailable {
		t.Fatalf("expected demo desktop startup to be unavailable: %s", w.Body)
	}
}
