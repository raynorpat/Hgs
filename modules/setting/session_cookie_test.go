package setting

import "testing"

func TestSessionCookieSecure(t *testing.T) {
	if !SessionCookieSecure("https://example.com/", false) {
		t.Fatal("https site left the cookie unset")
	}
	if !SessionCookieSecure("HTTPS://example.com/", false) {
		t.Fatal("HTTPS site left the cookie unset")
	}
	if SessionCookieSecure("http://example.com/", false) {
		t.Fatal("http site forced the cookie")
	}
	if !SessionCookieSecure("http://example.com/", true) {
		t.Fatal("configured http site cleared the cookie")
	}
}
