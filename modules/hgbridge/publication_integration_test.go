//go:build integration

package hgbridge

import (
	"context"
	"errors"
	"github.com/gogits/gogs/modules/hg"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestContributionNativePublication(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Skip("GOGS_TEST_HG unavailable")
	}
	gogs := buildGogs(t)
	root := t.TempDir()
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "managed", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h := &hgHarness{t: t, r: r, ctx: ctx}
	var denied, fail atomic.Bool
	var signals atomic.Int64
	c, err := NewController(func(context.Context, Grant) error {
		if denied.Load() {
			return ErrDenied
		}
		return nil
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(ctx)
	c.SetPublicationObserver(func(_ context.Context, id int64) error {
		if id != 42 {
			return ErrDenied
		}
		signals.Add(1)
		if fail.Load() {
			return errors.New("owned fixture sink unavailable")
		}
		return nil
	})
	_, env, revoke, err := c.Issue(Grant{Principal: Principal{UserID: 1}, RepoID: 42, CanWrite: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer revoke()
	config, err := TransportHookConfig(gogs, true)
	if err != nil {
		t.Fatal(err)
	}
	source, target := filepath.Join(root, "source"), filepath.Join(root, "target")
	h.must("", nil, nil, "init", source)
	h.must("", nil, nil, "init", target)
	h.must(source, nil, nil, "debugbuilddag", "+1")
	endpoint, _ := h.serve(target, config, env)
	push := []string{"push", "--config", "http_proxy.host=", endpoint}
	denied.Store(true)
	if _, err = h.hg(source, nil, nil, push...); err == nil || !hookRejected.MatchString(err.Error()) {
		t.Fatalf("writer gate did not reject the push through its hook: %v", err)
	}
	if signals.Load() != 0 {
		t.Fatal("failed transaction emitted committed publication")
	}
	denied.Store(false)
	h.must(source, nil, nil, push...)
	if signals.Load() != 1 {
		t.Fatalf("native committed push signals=%d", signals.Load())
	}
	h.must(source, nil, nil, "debugbuilddag", "--from-existing", "@next .")
	fail.Store(true)
	h.must(source, nil, nil, append(push, "--new-branch")...)
	if signals.Load() != 2 {
		t.Fatalf("failure sink was not reached: %d", signals.Load())
	}
	if n := h.must(target, nil, nil, "log", "-T", "{rev}\n"); n != "1\n0\n" {
		t.Fatalf("advisory failure changed committed history: %q", n)
	}
	if _, err = os.Stat(filepath.Join(target, ".hg", "journal")); !os.IsNotExist(err) {
		t.Fatalf("native transaction remains unresolved: %v", err)
	}
}
