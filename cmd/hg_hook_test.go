// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hgbridge"
)

var writeGrant = hgbridge.Grant{Principal: hgbridge.Principal{UserID: 7, TokenID: 2}, RepoID: 42, CanWrite: true}

func sessionEnv(env map[string]string) []string {
	out := []string{"PATH=/bin"}
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	return out
}

// recorder counts requests that reach it and answers with status.
func recorder(t *testing.T, status int, delay time.Duration) (*httptest.Server, *atomic.Int32) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
		if status == http.StatusTemporaryRedirect {
			http.Redirect(w, r, "/elsewhere", status)
			return
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func TestReaderHooksDenyWithoutController(t *testing.T) {
	srv, hits := recorder(t, http.StatusNoContent, 0)
	env := sessionEnv(map[string]string{
		hgbridge.EnvCapability: "anything",
		hgbridge.EnvControl:    strings.TrimPrefix(srv.URL, "http://"),
		hgbridge.EnvRepoID:     "42",
	})
	for _, e := range [][]string{nil, env} {
		if err := hgHook(context.Background(), []string{"deny", "pretxnopen"}, e); err == nil {
			t.Fatal("deny mode allowed a transaction")
		}
	}
	if hits.Load() != 0 {
		t.Fatal("deny mode contacted the control endpoint")
	}
	for _, args := range [][]string{nil, {"deny"}, {"allow", "pretxnopen"}, {"check", "pretxnopen", "x"}} {
		if err := hgHook(context.Background(), args, env); err == nil {
			t.Fatalf("hook arguments %q allowed a transaction", args)
		}
	}
}

func TestHookFailsClosed(t *testing.T) {
	defer func(d time.Duration) { hgHookTimeout = d }(hgHookTimeout)
	hgHookTimeout = 200 * time.Millisecond

	var deny atomic.Bool
	ctl, err := hgbridge.NewController(func(context.Context, hgbridge.Grant) error {
		if deny.Load() {
			return errors.New("revoked")
		}
		return nil
	}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	defer ctl.Close(context.Background())
	capability, env, revoke, err := ctl.Issue(writeGrant, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()

	with := func(k, v string) []string {
		m := map[string]string{}
		for kk, vv := range env {
			m[kk] = vv
		}
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
		return sessionEnv(m)
	}

	if err := RunHGHook(context.Background(), "pretxnclose", sessionEnv(env)); err != nil {
		t.Fatalf("authorized writer denied: %v", err)
	}
	if err := hgHook(context.Background(), []string{"check", "pretxnclose-phase"}, sessionEnv(env)); err != nil {
		t.Fatalf("authorized writer denied through the command: %v", err)
	}

	hung, _ := recorder(t, http.StatusNoContent, 5*time.Second)
	failing, _ := recorder(t, http.StatusInternalServerError, 0)
	redirect, _ := recorder(t, http.StatusTemporaryRedirect, 0)
	addr := func(s *httptest.Server) string { return strings.TrimPrefix(s.URL, "http://") }

	for name, e := range map[string][]string{
		"no env":            nil,
		"no capability":     with(hgbridge.EnvCapability, ""),
		"no control":        with(hgbridge.EnvControl, ""),
		"no repo":           with(hgbridge.EnvRepoID, ""),
		"bad repo":          with(hgbridge.EnvRepoID, "42x"),
		"wrong repo":        with(hgbridge.EnvRepoID, "43"),
		"wrong capability":  with(hgbridge.EnvCapability, capability[1:]),
		"remote control":    with(hgbridge.EnvControl, "example.com:80"),
		"unreachable":       with(hgbridge.EnvControl, "127.0.0.1:1"),
		"hung controller":   with(hgbridge.EnvControl, addr(hung)),
		"failing":           with(hgbridge.EnvControl, addr(failing)),
		"redirect":          with(hgbridge.EnvControl, addr(redirect)),
		"lowercase env key": {"gogs_hg_capability=" + capability, "gogs_hg_control=" + env[hgbridge.EnvControl], "gogs_hg_repo_id=42"},
	} {
		start := time.Now()
		if err := RunHGHook(context.Background(), "pretxnclose", e); err == nil {
			t.Errorf("%s: write allowed", name)
		}
		if time.Since(start) > 2*time.Second {
			t.Errorf("%s: hook did not respect its timeout", name)
		}
	}
	if err := RunHGHook(context.Background(), "txnclose", sessionEnv(env)); err == nil {
		t.Error("unknown hook allowed")
	}

	deny.Store(true)
	if err := RunHGHook(context.Background(), "pretxnclose", sessionEnv(env)); err == nil {
		t.Fatal("revoked writer allowed")
	}
	deny.Store(false)
	ctl.Close(context.Background())
	if err := RunHGHook(context.Background(), "pretxnclose", sessionEnv(env)); err == nil {
		t.Fatal("write allowed with the controller gone")
	}
}
