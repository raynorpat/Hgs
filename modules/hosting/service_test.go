package hosting

import (
	"context"
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestHostingRejectsPlaintextCredentials(t *testing.T) {
	s := &Service{}
	r := httptest.NewRequest("GET", "http://host/?cmd=capabilities", nil)
	r.SetBasicAuth("alice", "secret")
	w := httptest.NewRecorder()
	s.ServeRepository(w, r, "owner", "repo")
	if w.Code != 403 {
		t.Fatalf("plaintext credentials: %d", w.Code)
	}
	r = httptest.NewRequest("POST", "http://host/?cmd=unbundle", nil)
	w = httptest.NewRecorder()
	s.ServeRepository(w, r, "owner", "repo")
	if w.Code != 403 {
		t.Fatalf("plaintext push: %d", w.Code)
	}
}
func TestHostingTrustsOnlyConfiguredProxies(t *testing.T) {
	s := &Service{}
	if err := s.configure(Config{PublicURL: "https://example.com", TrustedProxyCIDRs: []string{"127.0.0.0/8"}}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		peer, header string
		tls, want    bool
	}{
		{"192.0.2.4:44", "https", false, false}, {"127.0.0.1:44", "https", false, true},
		{"127.0.0.1:44", "https,http", false, false}, {"127.0.0.1:44", "", false, false}, {"192.0.2.4:44", "", true, true},
	} {
		r := httptest.NewRequest("GET", "http://host/", nil)
		r.RemoteAddr = tc.peer
		r.Header.Set("X-Forwarded-Proto", tc.header)
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		if s.secure(r) != tc.want {
			t.Errorf("%+v", tc)
		}
	}
}
func TestHostingInvalidConfigBeforeStartup(t *testing.T) {
	for _, cfg := range []Config{{PublicURL: "bogus"}, {PublicURL: "https://example.com", TrustedProxyCIDRs: []string{"bad"}}, {PublicURL: "https://example.com", AppSubURL: "/../bad"}} {
		if s, err := New(context.Background(), cfg); err == nil {
			s.Shutdown(context.Background())
			t.Fatal("accepted invalid config")
		}
	}
}
