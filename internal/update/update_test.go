package update

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyChecksReleaseAndInstaller(t *testing.T) {
	const installer = "new installer"
	for _, tc := range []struct {
		name, tag, requested, body string
		prerelease                 bool
		want                       int
	}{
		{"verified", "v1.10.0", "v1.10.0", installer, false, 200},
		{"older", "v1.1.0", "v1.1.0", installer, false, 409},
		{"preview", "v1.10.0", "v1.10.0", installer, true, 409},
		{"release changed", "v1.10.1", "v1.10.0", installer, false, 409},
		{"checksum mismatch", "v1.10.0", "v1.10.0", "bad installer", false, 502},
		{"oversized response", "v1.10.0", "v1.10.0", installer + "!", false, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := t.TempDir()
			t.Setenv("XDG_CACHE_HOME", cache)
			t.Setenv("LocalAppData", cache)
			downloads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/latest":
					fmt.Fprintf(w, `{"tag_name":%q,"prerelease":%t,"assets":[{"name":%q,"size":%d,"digest":"sha256:%x","state":"uploaded"}]}`,
						tc.tag, tc.prerelease, setupName, len(installer), sha256.Sum256([]byte(installer)))
				case "/releases/download/" + tc.tag + "/" + setupName:
					downloads++
					fmt.Fprint(w, tc.body)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			var installed, stopped bool
			s := &service{
				api: server.URL + "/latest", repository: server.URL, current: "v1.2.0", client: server.Client(),
				install: func(path string) error {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != installer {
						t.Fatalf("launched unverified installer: %q, %v", data, err)
					}
					installed = true
					return nil
				},
				shutdown: func() { stopped = true },
			}
			request := func() *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				s.apply(w, httptest.NewRequest(http.MethodPost, "/api/update", strings.NewReader(fmt.Sprintf(`{"version":%q}`, tc.requested))))
				return w
			}
			w := request()
			wantInstall := tc.want == 200
			if w.Code != tc.want || installed != wantInstall || stopped != wantInstall {
				t.Fatalf("status=%d body=%q installed=%t shutdown=%t", w.Code, w.Body, installed, stopped)
			}
			if wantInstall && (request().Code != http.StatusConflict || downloads != 1) {
				t.Fatalf("repeated installation downloaded %d installers", downloads)
			}
			files, err := filepath.Glob(filepath.Join(cache, "Knooo", "updates", "setup-*"))
			if err != nil || (tc.want != 200 && len(files) > 0) {
				t.Fatalf("failed update left temporary files: %v, %v", files, err)
			}
		})
	}
}
