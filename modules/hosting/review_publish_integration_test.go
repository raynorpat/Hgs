//go:build integration

package hosting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
)

func publicationFixture(t *testing.T) (*Service, int64, MutationExpectation, string) {
	return publicationFixtureSelection(t, true)
}
func publicationFixtureSelection(t *testing.T, bookmark bool, sourceBranch ...string) (*Service, int64, MutationExpectation, string) {
	t.Helper()
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	s, owner, target := fixtureConfig(t, func(c *Config) { c.Mercurial.Executable = exe; c.Mercurial.MaxProcesses = 1 })
	s.hookExecutable = nativeHookBinary(t)
	source, err := models.CreateHgRepository(context.Background(), s.runner, s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "source", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	tp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), target.Id)
	sp, _ := models.RepositoryStoragePath(s.RepositoryRoot(), source.Id)
	local := filepath.Join(t.TempDir(), "local")
	nativeRun(t, s.runner, "", "init", local)
	if err = os.WriteFile(filepath.Join(local, "base"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	nativeRun(t, s.runner, local, "add", "base")
	nativeRun(t, s.runner, local, "commit", "-u", "alice", "-m", "base")
	nativeRun(t, s.runner, tp, "pull", local)
	nativeRun(t, s.runner, sp, "pull", local)
	nativeRun(t, s.runner, tp, "bookmark", "-r", "tip", "main")
	nativeRun(t, s.runner, tp, "bookmark", "-r", "tip", "untouched")
	branch := "default"
	if len(sourceBranch) > 0 {
		branch = sourceBranch[0]
		nativeRun(t, s.runner, local, "branch", branch)
	}
	if err = os.WriteFile(filepath.Join(local, "feature"), []byte("feature"), 0600); err != nil {
		t.Fatal(err)
	}
	nativeRun(t, s.runner, local, "add", "feature")
	nativeRun(t, s.runner, local, "commit", "-u", "alice", "-m", "feature")
	nativeRun(t, s.runner, sp, "pull", local)
	src, err := s.runner.ResolveReview(context.Background(), sp, hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBranch, Name: branch}})
	if err != nil {
		t.Fatal(err)
	}
	targetSelection := hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "main"}}
	if !bookmark {
		targetSelection.Ref = hg.RefSelection{Kind: hg.RefBranch, Name: "default"}
	}
	dst, err := s.runner.ResolveReview(context.Background(), tp, targetSelection)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := s.runner.SnapshotReview(context.Background(), src, dst, filepath.Join(t.TempDir(), "snapshot"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := s.runner.PrepareReviewMerge(context.Background(), snap, filepath.Join(t.TempDir(), "prepared"), "alice", "merge")
	if err != nil {
		t.Fatal(err)
	}
	return s, owner.Id, MutationExpectation{RepoID: target.Id, Selection: dst.Selection, Node: dst.Node, OperationID: "0123456789abcdef0123456789abcdef", Source: MutationEndpoint{RepoID: source.Id, Selection: src.Selection, Node: src.Node}, Prepared: prepared}, tp
}
func publish(t *testing.T, s *Service, actor int64, e MutationExpectation, tp string) error {
	t.Helper()
	return s.WithRepositoryMutation(context.Background(), actor, e, func(ctx context.Context, env map[string]string) error {
		return s.runner.PublishReview(ctx, hg.PublishReviewRequest{Prepared: e.Prepared, TargetPath: tp, Expected: hg.ReviewEndpoint{Path: tp, Selection: e.Selection, Node: e.Node}, OperationID: e.OperationID}, env)
	})
}
func TestReviewPublishUpdatesOnlySelectedBookmark(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	if err := publish(t, s, actor, e, tp); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]hg.NodeID{"main": e.Prepared.Result, "untouched": e.Node} {
		got, err := s.runner.ResolveReview(context.Background(), tp, hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: name}})
		if err != nil || got.Node != want {
			t.Fatalf("%s: %+v %v", name, got, err)
		}
	}
	nativeRun(t, s.runner, tp, "verify")
}
func TestReviewPublishRevocation(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	before := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n") + nativeRun(t, s.runner, tp, "bookmarks", "-T", "json")
	err := s.WithRepositoryMutation(context.Background(), actor, e, func(ctx context.Context, env map[string]string) error {
		user, err := models.GetUserById(actor)
		if err != nil {
			return err
		}
		user.IsActive = false
		if err = models.UpdateUser(user); err != nil {
			return err
		}
		return s.runner.PublishReview(ctx, s.mutationRequest(e), env)
	})
	if err == nil {
		t.Fatal("revoked publisher succeeded at native transaction boundary")
	}
	after := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n") + nativeRun(t, s.runner, tp, "bookmarks", "-T", "json")
	if after != before {
		t.Fatal("denied publication changed repository")
	}
}
func TestReviewPublishLostResponse(t *testing.T) { testReviewPublishLostResponse(t, false) }
func TestReviewPublishLostArtifactFinalizesCommittedFact(t *testing.T) {
	testReviewPublishLostResponse(t, true)
}
func testReviewPublishLostResponse(t *testing.T, loseArtifact bool) {
	s, actor, e, tp := publicationFixture(t)
	lost := errors.New("response lost")
	err := s.WithRepositoryMutation(context.Background(), actor, e, func(ctx context.Context, env map[string]string) error {
		if err := s.runner.PublishReview(ctx, hg.PublishReviewRequest{Prepared: e.Prepared, TargetPath: tp, Expected: hg.ReviewEndpoint{Path: tp, Selection: e.Selection, Node: e.Node}, OperationID: e.OperationID}, env); err != nil {
			return err
		}
		return lost
	})
	if !errors.Is(err, lost) {
		t.Fatalf("lost response: %v", err)
	}
	if _, err = s.Register(hgbridge.Grant{Principal: passwordPrincipal(t, mustPublicationUser(t, actor)), RepoID: e.RepoID, CanWrite: true}, func() {}); err == nil {
		t.Fatal("pending publication admitted native session")
	}
	before := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n")
	if loseArtifact {
		if err = os.RemoveAll(filepath.Dir(e.Prepared.Path)); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted, err := New(context.Background(), s.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Shutdown(context.Background())
	restarted.hookExecutable = s.hookExecutable
	if loseArtifact {
		op, err := models.ReviewPublicationStatus(e.OperationID)
		if err != nil || !op.Complete {
			t.Fatalf("lost artifact did not finalize observed result: %+v %v", op, err)
		}
	}
	if err = publish(t, restarted, actor, e, tp); err != nil {
		t.Fatal(err)
	}
	if after := nativeRun(t, restarted.runner, tp, "log", "-T", "{node}\n"); after != before {
		t.Fatal("retry duplicated history")
	}
}
func TestReviewPublishBusySourceSession(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	// A registered native process must prevent reservation until actual exit.
	release, err := s.Register(hgbridge.Grant{Principal: passwordPrincipal(t, mustPublicationUser(t, actor)), RepoID: e.Source.RepoID, CanWrite: true}, func() {})
	if err != nil {
		t.Fatal(err)
	}
	if err = publish(t, s, actor, e, tp); !errors.Is(err, ErrMutationBusy) {
		t.Fatalf("active source writer: %v", err)
	}
	release()
	if err = publish(t, s, actor, e, tp); err != nil {
		t.Fatal(err)
	}
}
func mustPublicationUser(t *testing.T, id int64) *models.User {
	t.Helper()
	u, e := models.GetUserById(id)
	if e != nil {
		t.Fatal(e)
	}
	return u
}

func TestReviewPublishStaleBeforeMutationReleasesReservations(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	nativeRun(t, s.runner, tp, "bookmark", "--delete", "main")
	if err := publish(t, s, actor, e, tp); !errors.Is(err, ErrStaleRepository) {
		t.Fatalf("stale: %v", err)
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("stranded reservations: %+v %v", rows, err)
	}
	op, err := models.ReviewPublicationStatus(e.OperationID)
	if err != nil || !op.Aborted || op.Started || op.Complete {
		t.Fatalf("abort state: %+v %v", op, err)
	}
}

func TestReviewPublishRejectsInvalidPreparedResultBeforeMutation(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	before := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n")
	e.Prepared.Mode = "invented"
	if err := publish(t, s, actor, e, tp); err == nil {
		t.Fatal("invalid prepared result accepted")
	}
	if after := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n"); after != before {
		t.Fatal("invalid prepared identity published history")
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("invalid request retained fences: %+v %v", rows, err)
	}
}

func TestReviewPublishChangedSourceRefusesBeforeMutation(t *testing.T) {
	s, actor, e, tp := publicationFixture(t)
	source, _ := models.RepositoryStoragePath(s.RepositoryRoot(), e.Source.RepoID)
	local := filepath.Join(t.TempDir(), "new-source")
	nativeRun(t, s.runner, "", "clone", source, local)
	if err := os.WriteFile(filepath.Join(local, "unreviewed"), []byte("unreviewed"), 0600); err != nil {
		t.Fatal(err)
	}
	nativeRun(t, s.runner, local, "add", "unreviewed")
	nativeRun(t, s.runner, local, "commit", "-u", "alice", "-m", "unreviewed")
	nativeRun(t, s.runner, source, "pull", local)
	before := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n")
	if err := publish(t, s, actor, e, tp); !errors.Is(err, ErrStaleRepository) {
		t.Fatalf("changed source: %v", err)
	}
	if after := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n"); after != before {
		t.Fatal("stale source published history")
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 0 {
		t.Fatalf("stale source reservations: %+v %v", rows, err)
	}
}

func TestReviewPublishShutdownTracksActivePublication(t *testing.T) {
	s, actor, e, _ := publicationFixture(t)
	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- s.WithRepositoryMutation(context.Background(), actor, e, func(ctx context.Context, _ map[string]string) error { close(entered); <-ctx.Done(); return ctx.Err() })
	}()
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("publication did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := s.Shutdown(ctx); err == nil {
		t.Fatal("expected interrupted shutdown")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled publication succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not await publication")
	}
	op, err := models.ReviewPublicationStatus(e.OperationID)
	if err != nil || !op.Started || op.Complete {
		t.Fatalf("incorrect shutdown recovery state: %+v %v", op, err)
	}
	rows, err := models.PendingReviewReservations()
	if err != nil || len(rows) != 2 {
		t.Fatalf("shutdown lost fences: %+v %v", rows, err)
	}
}

func TestReviewPublishRejectsCrossBranchFastForwardForgery(t *testing.T) {
	s, actor, e, tp := publicationFixtureSelection(t, true, "feature")
	if e.Prepared.Mode != "two_parent" {
		t.Fatal("fixture must prepare native cross-branch merge")
	}
	e.Prepared.Mode = "fast_forward"
	e.Prepared.Result = e.Source.Node
	e.Prepared.DestinationBranch = "feature"
	before := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n") + nativeRun(t, s.runner, tp, "bookmarks", "-T", "json")
	if err := s.inspector.ValidateReviewPublication(context.Background(), s.mutationRequest(e)); !errors.Is(err, hg.ErrNotFound) {
		t.Fatalf("cross-branch forged artifact validation: %v", err)
	}
	if err := publish(t, s, actor, e, tp); !errors.Is(err, hg.ErrNotFound) {
		t.Fatalf("cross-branch publication validation: %v", err)
	}
	if after := nativeRun(t, s.runner, tp, "log", "-T", "{node}\n") + nativeRun(t, s.runner, tp, "bookmarks", "-T", "json"); after != before {
		t.Fatal("forgery changed target")
	}
}
