package hosting

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hgbridge"
)

func TestHostingPasswordChangeRevokesOriginalGrant(t *testing.T) {
	s, user, repo := fixture(t)
	identity, err := models.AuthenticateRepositoryHTTPSIdentity("alice", "password")
	if err != nil {
		t.Fatal(err)
	}
	grant := hgbridge.Grant{Principal: hgbridge.Principal{UserID: identity.UserID, PasswordVersion: identity.PasswordVersion}, RepoID: repo.Id, CanWrite: true}
	canceled, cancel := context.WithCancel(context.Background())
	release, err := s.Register(grant, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err = s.authorize(context.Background(), grant); err != nil {
		t.Fatal(err)
	}
	token := &models.AccessToken{Uid: user.Id, Name: "unaffected"}
	if err = models.NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	user.Passwd = "changed-password"
	user.Salt = models.GetUserSalt()
	user.EncodePasswd()
	if err = models.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	if err = s.authorize(context.Background(), grant); err == nil {
		t.Fatal("password reset left old hook grant valid before scan")
	}
	tokenGrant := grant
	tokenGrant.Principal = hgbridge.Principal{UserID: user.Id, TokenID: token.Id}
	if err = s.authorize(context.Background(), tokenGrant); err != nil {
		t.Fatalf("password reset revoked independent token: %v", err)
	}
	missing := grant
	missing.Principal.PasswordVersion = ""
	if err = s.authorize(context.Background(), missing); err == nil {
		t.Fatal("missing password version allowed writer")
	}
	select {
	case <-canceled.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("password reset did not cancel transfer within monitor bound")
	}
}
func TestHostingBrowserReadRegistrationHasExplicitReadOnlyPath(t *testing.T) {
	s, user, repo := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	release, err := s.RegisterBrowserRead(user.Id, repo.Id, cancel)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	s.sessions.check()
	select {
	case <-ctx.Done():
		t.Fatal("valid browser read canceled")
	default:
	}
	user.IsActive = false
	if err = models.UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	s.sessions.check()
	select {
	case <-ctx.Done():
	default:
		t.Fatal("disabled browser account retained read")
	}
}
func TestHostingAuthenticationBackendFailureIsUnavailable(t *testing.T) {
	s, _, _ := fixture(t)
	invalid := httptest.NewRequest("GET", "https://example.test/?cmd=capabilities", nil)
	invalid.SetBasicAuth("alice", "wrong")
	w := httptest.NewRecorder()
	s.ServeRepository(w, invalid, "alice", "project")
	if w.Code != 401 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("invalid credential: %d", w.Code)
	}
	if err := models.CloseEngine(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"project", "missing"} {
		request := httptest.NewRequest("GET", "https://example.test/?cmd=capabilities", nil)
		request.SetBasicAuth("alice", "password")
		w = httptest.NewRecorder()
		s.ServeRepository(w, request, "alice", name)
		if w.Code < 500 || w.Header().Get("WWW-Authenticate") != "" {
			t.Fatalf("backend failure challenged credentials for %s: %d %v", name, w.Code, w.Header())
		}
	}
}
