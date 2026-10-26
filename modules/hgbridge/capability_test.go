// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuth is a mutable authorizer that records the grants it was asked about.
type fakeAuth struct {
	mu   sync.Mutex
	err  error
	seen []Grant
}

func (f *fakeAuth) authorize(_ context.Context, g Grant) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, g)
	return f.err
}

func (f *fakeAuth) set(err error) {
	f.mu.Lock()
	f.err = err
	f.mu.Unlock()
}

func newTestController(t *testing.T, auth *fakeAuth) (*Controller, *time.Time) {
	t.Helper()
	c, err := NewController(auth.authorize, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	c.now = func() time.Time { return now }
	return c, &now
}

var writer = Grant{Principal: Principal{UserID: 7, KeyID: 3}, RepoID: 42, CanWrite: true}

func TestCapabilityBoundToRepositoryAndActor(t *testing.T) {
	auth := &fakeAuth{}
	c, now := newTestController(t, auth)

	capability, env, revoke, err := c.Issue(writer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	if raw, err := base64.RawURLEncoding.DecodeString(capability); err != nil || len(raw) != 32 {
		t.Fatalf("capability is not 32 random bytes: %v", err)
	}
	if env[EnvCapability] != capability || env[EnvRepoID] != "42" || len(env) != 3 {
		t.Fatalf("unexpected session env keys: %d", len(env))
	}
	if host, _, err := net.SplitHostPort(env[EnvControl]); err != nil || host != "127.0.0.1" {
		t.Fatalf("control endpoint %q is not loopback", env[EnvControl])
	}
	for k := range env {
		if !strings.HasPrefix(k, "GOGS_HG_") {
			t.Fatalf("env key %q would be rejected by the runner", k)
		}
	}

	if err := c.Check(context.Background(), capability, 42, "pretxnclose"); err != nil {
		t.Fatalf("valid capability denied: %v", err)
	}
	if len(auth.seen) != 1 || auth.seen[0] != writer {
		t.Fatalf("authorizer saw %+v, want the stored grant", auth.seen)
	}

	other, _, revokeOther, err := c.Issue(writer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer revokeOther()
	if other == capability {
		t.Fatal("two sessions share a capability")
	}

	for name, check := range map[string]func() error{
		"wrong repository": func() error { return c.Check(context.Background(), capability, 43, "pretxnclose") },
		"unknown token":    func() error { return c.Check(context.Background(), capability+"x", 42, "pretxnclose") },
		"empty token":      func() error { return c.Check(context.Background(), "", 42, "pretxnclose") },
		"unknown hook":     func() error { return c.Check(context.Background(), capability, 42, "txnclose") },
	} {
		if err := check(); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: got %v, want ErrDenied", name, err)
		}
	}

	*now = now.Add(time.Hour)
	if err := c.Check(context.Background(), capability, 42, "pretxnclose"); !errors.Is(err, ErrDenied) {
		t.Fatalf("expired capability: got %v, want ErrDenied", err)
	}

	for name, g := range map[string]Grant{
		"read-only": {Principal: writer.Principal, RepoID: 42},
		"anonymous": {RepoID: 42, CanWrite: true},
		"no repo":   {Principal: writer.Principal, CanWrite: true},
	} {
		if capability, env, _, err := c.Issue(g, nil); err == nil || capability != "" || env != nil {
			t.Errorf("%s grant was issued a capability", name)
		}
	}
}

func TestCapabilityControlEndpoint(t *testing.T) {
	c, _ := newTestController(t, &fakeAuth{})
	capability, env, revoke, err := c.Issue(writer, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	url := "http://" + env[EnvControl] + ControlPath

	post := func(token string, body []byte) int {
		req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	good, _ := json.Marshal(HookRequest{Hook: "pretxnopen", RepoID: 42})

	if code := post(capability, good); code != http.StatusNoContent {
		t.Fatalf("valid request: status %d", code)
	}
	if code := post("nope", good); code != http.StatusForbidden {
		t.Fatalf("bad capability: status %d", code)
	}
	actor := []byte(`{"hook":"pretxnopen","repo_id":42,"user_id":1,"can_write":true}`)
	if code := post(capability, actor); code != http.StatusBadRequest {
		t.Fatalf("request supplying actor fields: status %d", code)
	}
	big := append([]byte(`{"hook":"pretxnopen","repo_id":42,"x":"`), bytes.Repeat([]byte("a"), maxControlBody)...)
	if code := post(capability, append(big, `"}`...)); code == http.StatusNoContent {
		t.Fatal("oversized body accepted")
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		t.Fatal("GET accepted")
	}
}

func TestRevocationBeforeTxnClose(t *testing.T) {
	auth := &fakeAuth{}
	c, _ := newTestController(t, auth)
	capability, env, revoke, err := c.Issue(writer, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := func() error { return c.Check(context.Background(), capability, 42, "pretxnclose") }

	if err := check(); err != nil {
		t.Fatalf("pretxnopen-time check: %v", err)
	}
	auth.set(errors.New("demoted to read access"))
	if err := check(); !errors.Is(err, ErrDenied) {
		t.Fatalf("demoted writer: got %v, want ErrDenied", err)
	}
	auth.set(nil)
	if err := check(); err != nil {
		t.Fatalf("restored writer: %v", err)
	}

	revoke()
	revoke()
	if err := check(); !errors.Is(err, ErrDenied) {
		t.Fatalf("revoked session: got %v, want ErrDenied", err)
	}

	if _, _, _, err := c.Issue(writer, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := c.Issue(writer, nil); err == nil {
		t.Fatal("closed controller issued a capability")
	}
	if conn, err := net.DialTimeout("tcp", env[EnvControl], time.Second); err == nil {
		conn.Close()
		t.Fatal("closed controller still listening")
	}
}

func TestAllowedCheckTouchesSession(t *testing.T) {
	auth := &fakeAuth{}
	c, _ := newTestController(t, auth)
	touches := 0
	capability, _, revoke, err := c.Issue(writer, func() { touches++ })
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()

	if err := c.Check(context.Background(), capability, 42, "pretxnclose"); err != nil || touches != 1 {
		t.Fatalf("allowed check: %v, %d touches; want 1", err, touches)
	}
	c.Check(context.Background(), capability, 43, "pretxnclose")
	auth.set(errors.New("demoted to read access"))
	c.Check(context.Background(), capability, 42, "pretxnclose")
	if touches != 1 {
		t.Errorf("denied checks touched the session: %d touches", touches)
	}
}
