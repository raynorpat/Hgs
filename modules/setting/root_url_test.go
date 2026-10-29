package setting

import "testing"

func TestNormalizeRootURL(t *testing.T) {
	for _, raw := range []string{"https://example.test/a/../b/", "https://example.test/a//b/", "https://example.test/%61/", "https://example.test/a%2fb/", "https://example.test/a?", "https://example.test/a#fragment"} {
		if _, _, err := NormalizeRootURL(raw); err == nil {
			t.Errorf("noncanonical root accepted %q", raw)
		}
	}
	for _, raw := range []string{"https://example.test/lab", "https://example.test/lab/"} {
		url, sub, err := NormalizeRootURL(raw)
		if err != nil || url != "https://example.test/lab/" || sub != "/lab" {
			t.Fatalf("valid sub-URL %q %q %v", url, sub, err)
		}
	}
}
