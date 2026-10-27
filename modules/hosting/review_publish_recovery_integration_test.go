//go:build integration

package hosting

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

func TestReviewPublishRecoversKilledNativeJournal(t *testing.T) {
	testKilledReviewPublication(t, false)
}
func TestReviewPublishRevokedInterruptedTargetReleasesSource(t *testing.T) {
	testKilledReviewPublication(t, true)
}
func testKilledReviewPublication(t *testing.T, revokeActor bool) {
	s, actor, e, tp := publicationFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			close(entered)
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
			}
			http.Error(w, "interrupted", http.StatusForbidden)
			return
		}
		s.controller.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	done := make(chan error, 1)
	go func() {
		done <- s.WithRepositoryMutation(ctx, actor, e, func(ctx context.Context, env map[string]string) error {
			// Test instrumentation pauses a native unbundle after journal creation;
			// production never installs transaction-close authorization hooks.
			hooks, err := hgbridge.HookConfig(s.hookExecutable, true)
			if err != nil {
				return err
			}
			f, err := os.OpenFile(env["GOGS_HG_PUBLICATION_CONFIG"], os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = f.WriteString("pretxnclose = " + hooks["hooks.pretxnopen"] + "\n")
			f.Close()
			if err != nil {
				return err
			}
			env[hgbridge.EnvControl] = strings.TrimPrefix(proxy.URL, "http://")
			return s.runner.PublishReview(ctx, s.mutationRequest(e), env)
		})
	}()
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("unbundle did not reach test close barrier")
	}
	if _, err := os.Stat(filepath.Join(tp, ".hg", "store", "journal")); err != nil {
		t.Fatalf("no native transaction journal at barrier: %v", err)
	}
	if revokeActor {
		u, err := models.GetUserById(actor)
		if err != nil {
			t.Fatal(err)
		}
		u.IsActive = false
		if err = models.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("killed native operation succeeded")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("native child did not stop")
	}
	if _, err := os.Stat(filepath.Join(tp, ".hg", "store", "journal")); err != nil {
		t.Fatalf("kill did not preserve native journal: %v", err)
	}
	if revokeActor {
		op, err := models.ReviewPublicationStatus(e.OperationID)
		if err != nil || !op.Failed || !op.RepairRequired || op.Complete {
			t.Fatalf("revoked interrupted outcome: %+v %v", op, err)
		}
		rows, err := models.PendingReviewReservations()
		if err != nil || len(rows) != 1 || rows[0].RepoID != e.RepoID {
			t.Fatalf("source held hostage: %+v %v", rows, err)
		}
		assertPublicationSourceReadable(t, s, e.Source.RepoID)
		if _, err = os.Stat(filepath.Join(tp, ".hg", "store", "journal")); err != nil {
			t.Fatal("revoked operation performed native journal recovery")
		}
		u, err := models.GetUserById(actor)
		if err != nil {
			t.Fatal(err)
		}
		u.IsActive = true
		if err = models.UpdateUser(u); err != nil {
			t.Fatal(err)
		}
		if err = publish(t, s, actor, e, tp); !errors.Is(err, models.ErrReviewPublicationTerminal) {
			t.Fatalf("terminal operation replayed: %v", err)
		}
		return
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(context.Background())
	restarted.hookExecutable = s.hookExecutable
	if err = publish(t, restarted, actor, e, tp); err != nil {
		t.Fatalf("original operation recovery: %v", err)
	}
	nativeRun(t, restarted.runner, tp, "verify")
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("reservations after recovery: %+v %v", rows, err)
	}
}

func TestReviewPublishRevocationAtBookmarkTransaction(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 2 {
			u, err := models.GetUserById(actor)
			if err != nil {
				http.Error(w, "model", 500)
				return
			}
			u.IsActive = false
			if err = models.UpdateUser(u); err != nil {
				http.Error(w, "model", 500)
				return
			}
		}
		s.controller.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	err := s.WithRepositoryMutation(context.Background(), actor, e, func(ctx context.Context, env map[string]string) error {
		env[hgbridge.EnvControl] = strings.TrimPrefix(proxy.URL, "http://")
		return s.runner.PublishReview(ctx, s.mutationRequest(e), env)
	})
	if err == nil {
		t.Fatal("revoked second transaction succeeded")
	}
	op, err := models.ReviewPublicationStatus(e.OperationID)
	if err != nil || op.Complete || !op.Started {
		t.Fatalf("false success: %+v %v", op, err)
	}
	got := nativeRun(t, s.runner, tp, "log", "-r", string(e.Prepared.Result), "-T", "{node}")
	if got != string(e.Prepared.Result) {
		t.Fatal("first authorized transaction missing")
	}
	current, err := s.runner.ResolveReview(context.Background(), tp, e.Selection)
	if err != nil || current.Node != e.Node {
		t.Fatalf("revoked bookmark moved: %+v %v", current, err)
	}
	if err = publish(t, s, actor, e, tp); !errors.Is(err, hgbridge.ErrDenied) {
		t.Fatalf("revoked recovery authority reused: %v", err)
	}
	if !op.Failed || op.RepairRequired {
		t.Fatalf("partial outcome not terminal: %+v", op)
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("quiescent partial result retained fences: %+v %v", rows, err)
	}
	assertPublicationSourceReadable(t, s, e.Source.RepoID)
	u, err := models.GetUserById(actor)
	if err != nil {
		t.Fatal(err)
	}
	u.IsActive = true
	if err = models.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if err = publish(t, s, actor, e, tp); !errors.Is(err, models.ErrReviewPublicationTerminal) {
		t.Fatalf("terminal partial publication replayed: %v", err)
	}
}
func TestReviewPublishNativeRecoveryLockOwnership(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	runner, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(t.TempDir(), "hgrc"), MaxProcesses: 2})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "repo")
	nativeRun(t, runner, "", "init", path)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child, err := runner.Start(ctx, path, []string{"debuglocks", "--set-lock", "--set-wlock"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Stdout.Close()
	defer child.Stderr.Close()
	child.Stdin.Close()
	go io.Copy(io.Discard, child.Stdout)
	go io.Copy(io.Discard, child.Stderr)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(path, ".hg", "store", "lock")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native lock owner never acquired store")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = runner.RecoverReviewPublication(context.Background(), path); err == nil {
		t.Fatal("recovery accepted a live native lock owner")
	}
	if _, err = os.Stat(filepath.Join(path, ".hg", "store", "lock")); err != nil {
		t.Fatal("live owner's lock removed")
	}
	cancel()
	if err = child.Wait(); err == nil {
		t.Fatal("expected killed native lock owner")
	}
	if err = runner.RecoverReviewPublication(context.Background(), path); err != nil {
		t.Fatalf("native stale-owner recovery: %v", err)
	}
	if err = hg.CheckReviewPublicationQuiescent(path); err != nil {
		t.Fatal(err)
	}
	nativeRun(t, runner, path, "verify")
}

func assertPublicationSourceReadable(t *testing.T, s *Service, id int64) {
	t.Helper()
	reader := &models.User{Name: "source-reader", Email: "source-reader@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reader); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	release, err := s.RegisterBrowserRead(reader.Id, id, func() {})
	if err != nil {
		t.Fatalf("unrelated source reader blocked by revoked publisher: %v", err)
	}
	release()
}
func TestReviewPublishRevokedAfterCommitFinalizesFact(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	lost := errors.New("lost response after revocation")
	err := s.WithRepositoryMutation(context.Background(), actor, e, func(ctx context.Context, env map[string]string) error {
		if err := s.runner.PublishReview(ctx, s.mutationRequest(e), env); err != nil {
			return err
		}
		u, err := models.GetUserById(actor)
		if err != nil {
			return err
		}
		u.IsActive = false
		if err = models.UpdateUser(u); err != nil {
			return err
		}
		return lost
	})
	if !errors.Is(err, lost) {
		t.Fatal(err)
	}
	op, err := models.ReviewPublicationStatus(e.OperationID)
	if err != nil || !op.Complete || op.Failed || op.RepairRequired {
		t.Fatalf("committed fact not finalized: %+v %v", op, err)
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("committed revoked outcome retained fences: %+v %v", rows, err)
	}
	current, err := s.runner.ResolveReview(context.Background(), tp, e.Selection)
	if err != nil || current.Node != e.Prepared.Result {
		t.Fatalf("historical completion false: %+v %v", current, err)
	}
	assertPublicationSourceReadable(t, s, e.Source.RepoID)
}
