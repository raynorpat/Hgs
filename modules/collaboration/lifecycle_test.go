package collaboration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

func TestCollaborationDeleteAndTransfer(t *testing.T) {
	f := newComparisonFixture(t, true)
	c := f.refresh()
	if _, err := models.AddPullComment(f.ctx, f.owner.Id, f.pull.ID, "private unmerged discussion", nil); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
	p, err := f.service.GetPull(f.ctx, f.owner.Id, f.target.Id, f.pull.Number)
	if err != nil {
		t.Fatalf("deleted-source audit is unavailable: %v", err)
	}
	if p.State != models.PullDeclined || p.Title != "" || p.Body != "" || p.AuthorID != 0 || p.CurrentComparisonID != 0 {
		t.Fatalf("unmerged payload retained: %+v", p)
	}
	if _, err := os.Lstat(filepath.Join(f.artifacts, c.ArtifactID)); !os.IsNotExist(err) {
		t.Fatalf("private snapshot not purged: %v", err)
	}
}

func TestCollaborationLifecycleRecovery(t *testing.T) {
	f := newComparisonFixture(t, true)
	old := f.refresh()
	f.write("later", "accepted later\n")
	f.run(f.local, "add", "later")
	node := f.commit("later comparison")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", node)
	c := f.refresh()
	interrupted := errors.New("simulated PR-finalization interruption")
	mergeFault = func(stage string) error {
		if stage == "mutated" {
			return interrupted
		}
		return nil
	}
	t.Cleanup(func() { mergeFault = nil })
	r, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "recover-lifecycle"))
	mergeFault = nil
	if !errors.Is(err, interrupted) {
		t.Fatalf("missing finalization interruption: %+v %v", r, err)
	}
	if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); !errors.Is(err, models.ErrReviewReserved) {
		t.Fatalf("published gap deletion admitted: %v", err)
	}
	if err := f.service.ReconcileOperations(f.ctx); err != nil {
		t.Fatal(err)
	}
	if !claimMergeWorker(r.OperationID) {
		t.Fatal("unexpected worker")
	}
	err = models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted)
	releaseMergeWorker(r.OperationID)
	if !errors.Is(err, ErrMergeBusy) {
		t.Fatalf("terminal worker tail not fenced: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.artifacts, old.ArtifactID)); err != nil {
		t.Fatalf("busy tail lost owner payload: %v", err)
	}
	if _, err := f.service.GetPull(f.ctx, f.owner.Id, f.target.Id, f.pull.Number); err == nil {
		t.Fatal("deleting source exposed retained history early")
	}
	// This is a DB close/reopen and actual hosting constructor restart, not an
	// OS-kill experiment. The injected closure needs no published provider.
	if err := f.host.Shutdown(f.ctx); err != nil {
		t.Fatal(err)
	}
	models.CloseEngine()
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	f.host, err = hosting.New(f.ctx, hosting.Config{Mercurial: hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(f.root, "config", "hgrc"), MaxProcesses: 1}, RepositoryRoot: filepath.Join(f.root, "repos"), ReviewArtifactRoot: f.artifacts, HostKeyPath: filepath.Join(f.root, "keys", "host"), PublicURL: "https://example.test", RepositoryCleanup: RepositoryCleanup(filepath.Join(f.root, "repos"), f.artifacts)})
	if err != nil {
		t.Fatal(err)
	}
	f.service = New(&pullProvider{host: f.host})
	if _, err := os.Stat(filepath.Join(f.artifacts, old.ArtifactID)); !os.IsNotExist(err) {
		t.Fatalf("old comparison retained: %v", err)
	}
	if _, _, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, old.ID); err == nil {
		t.Fatal("old payload became target-readable")
	}
	if _, _, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, c.ID); err != nil {
		t.Fatalf("accepted comparison lost: %v", err)
	}
	if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.target.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
	if _, err := models.LoadMergeOperation(r.OperationID); err != nil {
		t.Fatalf("denial binding erased: %v", err)
	}
	if err := models.ValidateMergePublication(f.ctx, f.owner.Id, r.OperationID, f.source.Id, f.target.Id, c.SourceNode, c.TargetNode, r.Node); err == nil {
		t.Fatal("deleted target operation became generic publication")
	}
	if _, err := os.Stat(filepath.Join(f.artifacts, c.ArtifactID)); !os.IsNotExist(err) {
		t.Fatalf("target artifact retained: %v", err)
	}
}

func TestCollaborationMissingMarkerRefusesRetry(t *testing.T) {
	f := newComparisonFixture(t)
	c := f.refresh()
	marker := filepath.Join(f.artifacts, c.ArtifactID, comparisonOwnerFile)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err == nil {
			t.Fatal("markerless allocation adopted")
		}
		rows, err := models.RepositoryPullDeletion(f.ctx, f.source.Id)
		if err != nil || len(rows) != 1 || len(rows[0].Comparisons) != 1 {
			t.Fatalf("retry lost ownership: %+v %v", rows, err)
		}
		if _, err := os.Stat(filepath.Join(f.artifacts, c.ArtifactID, "snapshot")); err != nil {
			t.Fatalf("unknown content changed: %v", err)
		}
	}
	// Explicit fixture-owner repair; production never fabricates a lost marker.
	if err := os.WriteFile(marker, comparisonOwnerBytes(c), 0600); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
}

func TestCollaborationMergedHistoryRetention(t *testing.T) {
	f := newComparisonFixture(t, true)
	c := f.refresh()
	if _, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "lifecycle-merged")); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
	p, err := f.service.GetPull(f.ctx, f.owner.Id, f.target.Id, f.pull.Number)
	if err != nil || p.State != models.PullMerged {
		t.Fatalf("accepted history lost: %+v %v", p, err)
	}
	if _, _, err := f.service.Comparison(f.ctx, f.owner.Id, p.ID, c.ID); err != nil {
		t.Fatalf("accepted content unavailable: %v", err)
	}
}
