package studio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopTopmost(t *testing.T) {
	a := &app{demo: true}
	const addr, token = "127.0.0.1:48763", "topmost-token"
	const endpoint = "http://" + addr + "/api/desktop-topmost"
	pinned := false
	calls := 0
	topmost := func(requested *bool) (bool, bool, error) {
		calls++
		if requested != nil {
			pinned = *requested
		}
		return true, pinned, nil
	}
	handler := a.Handler(addr, token, http.NotFoundHandler(), "", "", nil, topmost)
	headless := a.Handler(addr, token, http.NotFoundHandler(), "", "", nil, nil)
	request := func(h http.Handler, method, body, authorization, origin string, status int, response string) {
		t.Helper()
		r := httptest.NewRequest(method, endpoint, strings.NewReader(body))
		r.Header.Set("Authorization", authorization)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != status || response != "" && strings.TrimSpace(w.Body.String()) != response {
			t.Fatalf("%s %s received %d %q, want %d %q", method, body, w.Code, w.Body.String(), status, response)
		}
	}
	const authorization = "Bearer " + token
	request(handler, http.MethodGet, "", "", "", 403, "")
	request(handler, http.MethodPost, `{"pinned":true}`, "", "", 403, "")
	request(handler, http.MethodPost, `{"pinned":true}`, authorization, "http://other.example", 403, "")
	for _, body := range []string{`{}`, `null`, `{"pinned":null}`, `{"pinned":"true"}`, `{"pinned":`, `{"pinned":true,"extra":1}`, `{"pinned":false} {}`} {
		request(handler, http.MethodPost, body, authorization, "", 400, "")
	}
	if calls != 0 {
		t.Fatalf("rejected request reached the desktop callback %d times", calls)
	}
	request(headless, http.MethodGet, "", authorization, "", 200, `{"available":false,"pinned":false}`)
	request(headless, http.MethodPost, `{"pinned":true}`, authorization, "", 409, "")
	request(handler, http.MethodGet, "", authorization, "", 200, `{"available":true,"pinned":false}`)
	request(handler, http.MethodPost, `{"pinned":true}`, authorization, "", 200, `{"available":true,"pinned":true}`)
	request(handler, http.MethodGet, "", authorization, "", 200, `{"available":true,"pinned":true}`)
	request(handler, http.MethodPost, `{"pinned":false}`, authorization, "", 200, `{"available":true,"pinned":false}`)
}
