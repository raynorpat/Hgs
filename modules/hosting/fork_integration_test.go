//go:build integration

package hosting

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

func TestForkNativeLifecycle(t *testing.T) {
	s, _, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
	actor := &models.User{Name: "forker", Email: "forker@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(actor); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: actor.Id, RepoId: source.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "local")
	nativeRun(t, s.runner, "", "init", local)
	os.WriteFile(filepath.Join(local, "base"), []byte("base"), 0600)
	nativeRun(t, s.runner, local, "add", "base")
	nativeRun(t, s.runner, local, "commit", "-u", "fixture", "-m", "base")
	sp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), source.Id)
	nativeRun(t, s.runner, sp, "pull", local)
	fork, err := s.ForkRepository(context.Background(), actor.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	fp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), fork.Id)
	if nativeRun(t, s.runner, sp, "log", "-T", "{node}") != nativeRun(t, s.runner, fp, "log", "-T", "{node}") {
		t.Fatal("history not copied")
	}
	if !fork.IsPrivate || fork.State != models.RepositoryReady || fork.NumIssues != 0 || fork.NumStars != 0 || fork.NumWatches != 0 {
		t.Fatalf("fork metadata: %+v", fork)
	}
	relation, err := models.GetForkRelation(fork.Id)
	if err != nil || relation.SourceID != source.Id {
		t.Fatalf("relation %+v %v", relation, err)
	}
	// Native clone and push to the newly ready fork use real HTTP capability hooks.
	s.hookExecutable = nativeHookBinary(t)
	s.http.Close()
	s.http, err = hgbridge.NewHTTPBridge(s.runner, s.controller, s.hookExecutable)
	if err != nil {
		t.Fatal(err)
	}
	s.http.Registrar = s
	server := httptest.NewTLSServer(s.HTTP())
	defer server.Close()
	remote, _ := url.Parse(server.URL + "/forker/fork")
	remote.User = url.UserPassword("forker", "password")
	client, err := hg.NewRunner(hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(t.TempDir(), "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	copy := filepath.Join(t.TempDir(), "copy")
	nativeRun(t, client, "", "--config", "http_proxy.host=", "clone", "--insecure", remote.String(), copy)
	os.WriteFile(filepath.Join(copy, "feature"), []byte("feature"), 0600)
	nativeRun(t, client, copy, "add", "feature")
	nativeRun(t, client, copy, "commit", "-u", "forker", "-m", "feature")
	nativeRun(t, client, copy, "--config", "http_proxy.host=", "push", "--insecure", remote.String())
	if strings.Contains(nativeRun(t, s.runner, sp, "log", "-T", "{desc}"), "feature") {
		t.Fatal("fork shares mutable source")
	}
	child, err := s.ForkRepository(context.Background(), actor.Id, fork.Id, models.CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "child"})
	if err != nil {
		t.Fatal(err)
	}
	cp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), child.Id)
	if !strings.Contains(nativeRun(t, s.runner, cp, "log", "-T", "{desc}"), "feature") {
		t.Fatal("fork-of-fork copied root instead")
	}
	if err = models.DeleteAccess(&models.Access{UserId: actor.Id, RepoId: source.Id}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.resolve(context.Background(), hgbridge.Principal{UserID: actor.Id}, "forker", "fork"); err == nil {
		t.Fatal("native resolver bypassed ancestor")
	}
	if err = client.Run(context.Background(), "", []string{"--config", "http_proxy.host=", "clone", "--insecure", remote.String(), filepath.Join(t.TempDir(), "revoked")}, nil, nil); err == nil {
		t.Fatal("native clone after ancestor revocation succeeded")
	}
	if _, err = models.ForkRepositoryStatus(context.Background(), actor.Id, child.Id); err == nil {
		t.Fatal("revoked status exposed child")
	}
}

func TestForkNativeAuthorityAndFence(t *testing.T) {
	for _, stage := range []string{"allocated", "prepared"} {
		t.Run(stage, func(t *testing.T) {
			s, owner, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			released, err := s.RegisterBrowserRead(owner.Id, source.Id, cancel)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.ForkRepository(ctx, owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "busy"})
			if !errors.Is(err, ErrMutationBusy) {
				t.Fatalf("busy read: %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("fork cancelled existing session")
			}
			released()
			var dest int64
			forkTestHook = func(at string, op models.ForkProvisioning) {
				if at != stage {
					return
				}
				dest = op.RepositoryID
				if _, e := s.RegisterBrowserRead(owner.Id, source.Id, func() {}); e == nil {
					t.Error("admitted read through fork fence")
				}
				if e := models.DeleteHgRepository(context.Background(), s.RepositoryRoot(), source.Id, s.DrainRepository); e == nil {
					t.Error("deleted reserved source")
				}
				if _, e := models.BeginReviewPublication(models.ReviewPublication{ID: "review-conflict-id", ActorID: owner.Id}, []int64{source.Id}); e == nil {
					t.Error("review acquired fork source")
				}
				if e := models.ForkOwnerAllowed(owner.Id, owner.Id); e != nil {
					t.Error(e)
				}
				owner.IsActive = false
				if e := models.UpdateUser(owner); e != nil {
					t.Error(e)
				}
			}
			t.Cleanup(func() { forkTestHook = nil })
			_, err = s.ForkRepository(context.Background(), owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "revoked"})
			forkTestHook = nil
			if err == nil || dest == 0 {
				t.Fatalf("revoked fork: %d %v", dest, err)
			}
			if _, e := models.GetRepositoryById(dest); e != models.ErrRepoNotExist {
				t.Fatalf("half-ready fork: %v", e)
			}
			path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), dest)
			if _, e := os.Stat(path); !os.IsNotExist(e) {
				t.Fatalf("owned cleanup: %v", e)
			}
			pending, e := models.PendingForkProvisioning()
			if e != nil || len(pending) != 0 {
				t.Fatalf("claim retained: %v %v", pending, e)
			}
			if s.sessions.reservations[source.Id] != "" {
				t.Fatal("in-memory claim retained")
			}
			owner.IsActive = true
			models.UpdateUser(owner)
			if err = models.DeleteHgRepository(context.Background(), s.RepositoryRoot(), source.Id, s.DrainRepository); err != nil {
				t.Fatalf("source cannot be deleted after rollback: %v", err)
			}
		})
	}
}

func TestForkNativeSourceJournalNeverRecovered(t *testing.T) {
	s, _, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
	actor := &models.User{Name: "journal-reader", Email: "journal-reader@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(actor); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: actor.Id, RepoId: source.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	sp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), source.Id)
	journal := filepath.Join(sp, ".hg", "store", "journal")
	os.WriteFile(journal, []byte("unrelated interrupted write"), 0600)
	_, err := s.ForkRepository(context.Background(), actor.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "blocked"})
	if err == nil {
		t.Fatal("fork accepted dirty source")
	}
	b, e := os.ReadFile(journal)
	if e != nil || string(b) != "unrelated interrupted write" {
		t.Fatalf("source journal mutated: %s %v", b, e)
	}
}

func TestForkNativeWriterIsNeverCancelled(t *testing.T) {
	s, owner, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
	hook := nativeHookBinary(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	s.http.Close()
	s.controller.Close(context.Background())
	ctl, err := hgbridge.NewController(func(ctx context.Context, g hgbridge.Grant) error {
		once.Do(func() {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
			}
		})
		return s.authorize(ctx, g)
	}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	s.controller = ctl
	s.http, err = hgbridge.NewHTTPBridge(s.runner, ctl, hook)
	if err != nil {
		t.Fatal(err)
	}
	s.http.Registrar = s
	server := httptest.NewTLSServer(s.HTTP())
	defer server.Close()
	remote, _ := url.Parse(server.URL + "/alice/project")
	remote.User = url.UserPassword("alice", "password")
	client, err := hg.NewRunner(hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(t.TempDir(), "hgrc"), SessionTimeout: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "client")
	nativeRun(t, client, "", "init", local)
	os.WriteFile(filepath.Join(local, "writer"), []byte("writer"), 0600)
	nativeRun(t, client, local, "add", "writer")
	nativeRun(t, client, local, "commit", "-u", "alice", "-m", "writer")
	done := make(chan error, 1)
	go func() {
		done <- client.Run(context.Background(), local, []string{"--config", "http_proxy.host=", "push", "--insecure", remote.String()}, nil, nil)
	}()
	select {
	case <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("native writer never reached gate")
	}
	if _, err = s.ForkRepository(context.Background(), owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "busy-fork"}); !errors.Is(err, ErrMutationBusy) {
		t.Fatalf("writer fence: %v", err)
	}
	unblock.Do(func() { close(release) })
	select {
	case err = <-done:
		if err != nil {
			t.Fatalf("authorized writer disrupted: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("native writer did not finish")
	}
}

func TestForkStartupOwnedRollbackAndCollision(t *testing.T) {
	s, owner, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
	op, repo, err := models.BeginForkProvisioning(owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "restart"})
	if err != nil {
		t.Fatal(err)
	}
	if err = models.PrepareForkStorage(context.Background(), s.runner, s.RepositoryRoot(), op); err != nil {
		t.Fatal(err)
	}
	recovery := &Service{sessions: newRegistry(), cfg: s.cfg}
	recovery.sessions.reservations[source.Id] = "unrelated-review-owner"
	if err = recovery.reconcileForksAtStartup(); err == nil {
		t.Fatal("overwrote unknown reservation owner")
	}
	if recovery.sessions.reservations[source.Id] != "unrelated-review-owner" {
		t.Fatal("released unrelated claim")
	}
	delete(recovery.sessions.reservations, source.Id)
	if err = recovery.reconcileForksAtStartup(); err != nil {
		t.Fatal(err)
	}
	if _, err = models.GetRepositoryById(repo.Id); err != models.ErrRepoNotExist {
		t.Fatal("startup published half-fork", err)
	}
	path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), repo.Id)
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("startup failed owned cleanup", err)
	}
}

func TestForkNativeShutdownCancelsBeforeReady(t *testing.T) {
	s, owner, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
	entered := make(chan models.ForkProvisioning, 1)
	release := make(chan struct{})
	forkTestHook = func(stage string, op models.ForkProvisioning) {
		if stage == "prepared" {
			entered <- op
			<-release
		}
	}
	t.Cleanup(func() { forkTestHook = nil })
	done := make(chan error, 1)
	go func() {
		_, err := s.ForkRepository(context.Background(), owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "shutdown-fork"})
		done <- err
	}()
	var op models.ForkProvisioning
	select {
	case op = <-entered:
	case <-time.After(30 * time.Second):
		t.Fatal("fork never prepared")
	}
	cancelled := make(chan struct{})
	s.sessions.mu.Lock()
	for entry := range s.sessions.sessions {
		if entry.grant.RepoID == source.Id {
			original := entry.cancel
			var once sync.Once
			entry.cancel = func() { original(); once.Do(func() { close(cancelled) }) }
		}
	}
	s.sessions.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- s.Shutdown(ctx) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not cancel tracked fork")
	}
	close(release)
	if err := <-done; err == nil {
		t.Fatal("shutdown fork published")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not drain fork")
	}
	forkTestHook = nil
	if _, err := models.GetRepositoryById(op.RepositoryID); err != models.ErrRepoNotExist {
		t.Fatal("cancelled fork ready", err)
	}
}

func TestForkAsyncRequestEndAndRevocation(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		t.Run(fmt.Sprint(revoke), func(t *testing.T) {
			s, owner, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG"); c.Mercurial.MaxProcesses = 1 })
			actor := &models.User{Name: "async-actor", Email: "async@example.test", Passwd: "password", IsActive: true}
			if err := models.CreateUser(actor); err != nil {
				t.Fatal(err)
			}
			if err := models.AddAccess(&models.Access{UserId: actor.Id, RepoId: source.Id, Mode: models.READABLE}); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{})
			release := make(chan struct{})
			var unblock sync.Once
			t.Cleanup(func() { unblock.Do(func() { close(release) }); forkTestHook = nil })
			forkTestHook = func(stage string, op models.ForkProvisioning) {
				if stage == "allocated" {
					close(entered)
					<-release
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			pending, err := s.StartForkRepository(ctx, actor.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "async-fork"})
			if err != nil {
				t.Fatal(err)
			}
			cancel() // Request lifetime ends; the managed worker retains only identity.
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("worker not admitted")
			}
			if state, err := models.ForkRepositoryStatus(context.Background(), actor.Id, pending.Id); err != nil || state != models.RepositoryProvisioning {
				t.Fatalf("pending status %s %v", state, err)
			}
			if _, err := models.ForkRepositoryStatus(context.Background(), owner.Id, pending.Id); err == nil {
				t.Fatal("other actor saw pending operation")
			}
			s.sessions.mu.Lock()
			var done <-chan struct{}
			for entry := range s.sessions.sessions {
				if entry.grant.RepoID == source.Id {
					done = entry.done
				}
			}
			s.sessions.mu.Unlock()
			if _, err = s.StartForkRepository(context.Background(), actor.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: actor.Id, Name: "duplicate"}); !errors.Is(err, ErrMutationBusy) {
				t.Fatalf("duplicate admission %v", err)
			}
			if revoke {
				if err = models.DeleteAccess(&models.Access{UserId: actor.Id, RepoId: source.Id}); err != nil {
					t.Fatal(err)
				}
			}
			unblock.Do(func() { close(release) })
			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("async worker did not stop")
			}
			forkTestHook = nil
			result, err := models.GetRepositoryById(pending.Id)
			if revoke {
				if err != models.ErrRepoNotExist {
					t.Fatalf("revoked async fork published %+v %v", result, err)
				}
			} else if err != nil || result.State != models.RepositoryReady {
				t.Fatalf("request end cancelled managed fork: %+v %v", result, err)
			}
		})
	}
}

func TestForkFailedOperationCannotResume(t *testing.T) {
	s, owner, source := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = os.Getenv("GOGS_TEST_HG") })
	var op models.ForkProvisioning
	forkTestHook = func(stage string, current models.ForkProvisioning) {
		if stage == "prepared" {
			op = current
			path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), op.RepositoryID)
			if err := os.WriteFile(filepath.Join(path, ".hg", "gogs-storage"), []byte("foreign allocation"), 0600); err != nil {
				t.Error(err)
			}
		}
	}
	t.Cleanup(func() { forkTestHook = nil })
	_, err := s.ForkRepository(context.Background(), owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "failed"})
	forkTestHook = nil
	if err == nil {
		t.Fatal("unknown final storage published")
	}
	pending, err := models.PendingForkProvisioning()
	if err != nil || len(pending) != 1 || !pending[0].Failed {
		t.Fatalf("stopped operation not recorded: %+v %v", pending, err)
	}
	if _, err = models.ForkRepositoryStatus(context.Background(), owner.Id, op.RepositoryID); err == nil {
		t.Fatal("failed operation shown as provisioning")
	}
	if s.sessions.reservations[source.Id] != op.ID {
		t.Fatal("uncertain source claim released")
	}
	path, _ := models.RepositoryStoragePath(s.RepositoryRoot(), op.RepositoryID)
	marker := filepath.Join(path, ".hg", "gogs-storage")
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "foreign allocation" {
		t.Fatalf("unknown storage removed: %s %v", data, err)
	}
	// Even after an operator restores ownership, startup may only roll back. A
	// terminal attempt cannot be reused as an authorization to publish it.
	if err = os.WriteFile(marker, []byte(fmt.Sprintf("%d %s\n", op.RepositoryID, op.StorageNonce)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = models.PublishFork(context.Background(), s.RepositoryRoot(), pending[0]); err == nil {
		t.Fatal("failed operation resumed publication")
	}
}
