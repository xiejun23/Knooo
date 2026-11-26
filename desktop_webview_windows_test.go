package main

import "testing"

func TestDesktopWebViewTrustBoundary(t *testing.T) {
	const origin = "http://127.0.0.1:48763"
	for _, tc := range []struct {
		uri   string
		local bool
	}{
		{origin + "/#open=token", true},
		{"blob:" + origin + "/report", true},
		{"http://127.0.0.1:48764/", false},
		{"http://localhost:48763/", false},
		{"https://127.0.0.1:48763/", false},
		{"http://127.0.0.1:48763@example.test/", false},
		{"http://user@127.0.0.1:48763/", false},
		{"http://127.0.0.1:48763.example.test/", false},
		{"blob:https://example.test/report", false},
		{"file:///C:/Windows/", false},
		{"javascript:alert(1)", false},
		{"mailto:feedback@example.test", false},
	} {
		if got := localWebViewURI(origin, tc.uri); got != tc.local {
			t.Errorf("localWebViewURI(%q) = %v, want %v", tc.uri, got, tc.local)
		}
	}

	if !webViewMailto("mailto:feedback@example.test?subject=Knooo") {
		t.Error("feedback email link should remain available")
	}
	for _, uri := range []string{"mailto:", "mailto://example.test", "file:///C:/Windows/", "other:feedback@example.test"} {
		if webViewMailto(uri) {
			t.Errorf("webViewMailto(%q) accepted a non-email URI", uri)
		}
	}
}
