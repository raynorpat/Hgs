package collaboration

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

// Restore uses the original absolute locations in an isolated test-owned tree:
// durable prepared-operation expectations intentionally keep those paths.
func (f *comparisonFixture) coldRestore() {
	f.t.Helper()
	if err := f.host.Shutdown(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	if err := models.CloseEngine(); err != nil {
		f.t.Fatal(err)
	}
	backup := filepath.Join(f.t.TempDir(), "snapshot")
	if err := os.CopyFS(backup, os.DirFS(f.root)); err != nil {
		f.t.Fatal(err)
	}
	// f.root is created solely by newComparisonFixture with t.TempDir.
	if err := os.RemoveAll(f.root); err != nil {
		f.t.Fatal(err)
	}
	if err := os.CopyFS(f.root, os.DirFS(backup)); err != nil {
		f.t.Fatal(err)
	}
	if err := models.NewEngine(); err != nil {
		f.t.Fatal(err)
	}
	var err error
	f.host, err = hosting.New(f.ctx, hosting.Config{Mercurial: hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(f.root, "config", "hgrc"), MaxProcesses: 1}, RepositoryRoot: filepath.Join(f.root, "repos"), ReviewArtifactRoot: f.artifacts, HostKeyPath: filepath.Join(f.root, "keys", "host"), PublicURL: "https://example.test", RepositoryCleanup: RepositoryCleanup(filepath.Join(f.root, "repos"), f.artifacts)})
	if err != nil {
		f.t.Fatal(err)
	}
	f.service = New(&pullProvider{host: f.host})
	if err := ReconcileComparisons(f.ctx, f.host); err != nil {
		f.t.Fatal(err)
	}
}

func TestColdRestorePullArtifacts(t *testing.T) {
	t.Run("accepted-deleted-source", func(t *testing.T) {
		f := newComparisonFixture(t, true)
		c := f.refresh()
		result, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "backup-accepted"))
		if err != nil || result.State != "merged" {
			t.Fatalf("merge %+v %v", result, err)
		}
		if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
			t.Fatal(err)
		}
		f.coldRestore()
		var b bytes.Buffer
		if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, c.ID, "source", "feature", &b); err != nil || b.String() != "first\n" {
			t.Fatalf("accepted restored file %q %v", b.String(), err)
		}
		if err := f.service.ReadComparisonFile(f.ctx, 0, f.pull.ID, c.ID, "source", "feature", &b); err == nil {
			t.Fatal("private restored history exposed")
		}
		if _, err := os.Stat(filepath.Join(f.artifacts, c.ArtifactID, comparisonOwnerFile)); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("prepared-operation", func(t *testing.T) {
		f := newComparisonFixture(t)
		op, _ := f.preparedOperation("backup-prepared")
		f.coldRestore()
		if _, err := os.Stat(filepath.Join(preparedMergeRoot(f.host), op.ID, preparedOwnerFile)); err != nil {
			t.Fatal(err)
		}
		if err := f.service.ReconcileOperations(f.ctx); err != nil {
			t.Fatal(err)
		}
		done, err := models.LoadMergeOperation(op.ID)
		if err != nil || done.State != models.MergeFinalized || done.ResultNode != op.ResultNode {
			t.Fatalf("restored operation changed %+v %v", done, err)
		}
		if got := f.bookmarkNode(f.targetPath, "destination"); got != string(op.ResultNode) {
			t.Fatalf("restored result %s", got)
		}
	})
}
