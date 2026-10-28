package middleware

import "testing"

func TestLoginRedirect(t *testing.T) {
	for _, cookie := range []string{"//evil.com", "/%2Fevil.com", `/\evil.com`, "/%5Cevil.com", "https://evil.com", "", "%"} {
		if path, ok := LoginRedirect(cookie); ok {
			t.Errorf("accepted %q as %q", cookie, path)
		}
	}
	for _, cookie := range []string{"/", "/owner/repo", "/gogs/owner/repo", "%2Fowner%2Frepo"} {
		path, ok := LoginRedirect(cookie)
		if !ok || (cookie != "%2Fowner%2Frepo" && path != cookie) {
			t.Errorf("rejected %q (%q, %v)", cookie, path, ok)
		}
	}
	if path, ok := LoginRedirect("%2Fowner%2Frepo"); !ok || path != "/owner/repo" {
		t.Fatalf("unescape: %q %v", path, ok)
	}
}
