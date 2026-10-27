package collaboration

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/setting"
)

type comparisonFixture struct {
	t                                              *testing.T
	ctx                                            context.Context
	root, local, sourcePath, targetPath, artifacts string
	host                                           *hosting.Service
	service                                        *Service
	owner                                          *models.User
	source, target                                 *models.Repository
	pull                                           models.PullRequest
	base, feature                                  string
}

func newComparisonFixture(t *testing.T, private ...bool) *comparisonFixture {
	t.Helper()
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Skip("GOGS_TEST_HG required for native comparison tests")
	}
	f := &comparisonFixture{t: t, ctx: context.Background(), root: t.TempDir()}
	oldDB, oldLog := models.DbCfg, setting.LogRootPath
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(f.root, "test.db"))
	setting.LogRootPath = f.root
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { models.CloseEngine(); models.DbCfg = oldDB; setting.LogRootPath = oldLog })
	f.owner = &models.User{Name: "comparison-owner", Email: "comparison@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(f.owner); err != nil {
		t.Fatal(err)
	}
	f.artifacts = filepath.Join(f.root, "comparisons")
	var err error
	f.host, err = hosting.New(f.ctx, hosting.Config{Mercurial: hg.Config{Executable: exe, ConfigPath: filepath.Join(f.root, "config", "hgrc"), MaxProcesses: 1}, RepositoryRoot: filepath.Join(f.root, "repos"), ReviewArtifactRoot: f.artifacts, HostKeyPath: filepath.Join(f.root, "keys", "host"), PublicURL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := f.host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	if err := ReconcileComparisons(f.ctx, f.host); err != nil {
		t.Fatal(err)
	}
	f.service = New(&pullProvider{host: f.host})
	f.target, err = models.CreateHgRepository(f.ctx, f.host.Runner(), f.host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: f.owner.Id, Name: "target", Private: len(private) > 0 && private[0]})
	if err != nil {
		t.Fatal(err)
	}
	f.targetPath, _ = models.RepositoryStoragePath(f.host.RepositoryRoot(), f.target.Id)
	f.local = filepath.Join(f.root, "local")
	f.run("", "init", f.local)
	f.write("base", "base\n")
	f.run(f.local, "add", "base")
	f.base = f.commit("base")
	f.run(f.targetPath, "pull", f.local)
	f.targetNodeBookmark(f.targetPath, "destination", f.base)
	f.source, err = f.service.Fork(f.ctx, f.owner.Id, ForkOptions{SourceID: f.target.Id, OwnerID: f.owner.Id, Name: "source"})
	if err != nil {
		t.Fatal(err)
	}
	f.sourcePath, _ = models.RepositoryStoragePath(f.host.RepositoryRoot(), f.source.Id)
	f.write("feature", "first\n")
	f.run(f.local, "add", "feature")
	f.feature = f.commit("feature")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", f.feature)
	f.pull, err = f.service.CreatePull(f.ctx, f.owner.Id, NewPull{SourceRepoID: f.source.Id, TargetRepoID: f.target.Id, Source: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "proposal"}}, Target: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "destination"}}, Title: "Comparison"})
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *comparisonFixture) run(path string, args ...string) string {
	f.t.Helper()
	var b bytes.Buffer
	if err := f.host.Runner().Run(f.ctx, path, args, nil, &b); err != nil {
		f.t.Fatalf("hg %v: %v", args, err)
	}
	return strings.TrimSpace(b.String())
}
func (f *comparisonFixture) write(name, data string) {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.local, name), []byte(data), 0600); err != nil {
		f.t.Fatal(err)
	}
}
func (f *comparisonFixture) commit(message string) string {
	f.t.Helper()
	f.run(f.local, "commit", "-u", "fixture", "-m", message)
	return f.run(f.local, "log", "-r", ".", "-T", "{node}")
}
func (f *comparisonFixture) targetNodeBookmark(path, name, node string) {
	f.t.Helper()
	f.run(path, "bookmark", "-f", "-r", node, name)
}
func (f *comparisonFixture) refresh() models.PullComparison {
	f.t.Helper()
	c, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if c.State != "ready" {
		f.t.Fatalf("comparison not ready: %+v", c)
	}
	return c
}

// Removing private snapshots or reusing mutable refs leaks the feature into a
// fresh target clone or changes the content of a historical comparison.
func TestPullComparisonIsolatedFromTarget(t *testing.T) {
	f := newComparisonFixture(t)
	c := f.refresh()
	if c.SourceNode != hg.NodeID(f.feature) || c.TargetNode != hg.NodeID(f.base) || c.BaseNode != hg.NodeID(f.base) {
		t.Fatalf("wrong pins %+v", c)
	}
	clone := filepath.Join(f.root, "target-clone")
	f.run("", "clone", "--pull", "-U", f.targetPath, clone)
	if got := f.run(clone, "log", "-T", "{node}"); got != f.base {
		t.Fatalf("review published history %s", got)
	}
	if _, err := os.Stat(filepath.Join(f.targetPath, "feature")); !os.IsNotExist(err) {
		t.Fatalf("live target checkout: %v", err)
	}
	f.write("feature", "second\n")
	next := f.commit("second")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", next)
	newer := f.refresh()
	if newer.ID == c.ID || newer.Revision != c.Revision+1 || newer.SourceNode != hg.NodeID(next) {
		t.Fatalf("no new immutable version %+v", newer)
	}
	var b bytes.Buffer
	if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, c.ID, "source", "feature", &b); err != nil || b.String() != "first\n" {
		t.Fatalf("old bytes %q %v", b.String(), err)
	}
	old, review, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, c.ID)
	if err != nil || old.SourceNode != c.SourceNode || review.Source != c.SourceNode || !strings.Contains(string(review.Diff.Text), "+first") {
		t.Fatalf("old comparison changed %+v %v", old, err)
	}
	if got := f.run(f.targetPath, "log", "-T", "{node}"); got != f.base {
		t.Fatalf("refresh published history %s", got)
	}
}

// Deleted/ambiguous selectors must replace merge eligibility, while destination
// movement creates a distinct immutable comparison rather than changing pins.
func TestPullComparisonMovesAndAmbiguity(t *testing.T) {
	f := newComparisonFixture(t)
	first := f.refresh()
	f.run(f.local, "update", "-C", "-r", f.base)
	f.write("destination", "target\n")
	f.run(f.local, "add", "destination")
	target := f.commit("target")
	f.run(f.targetPath, "pull", "-r", target, f.local)
	f.targetNodeBookmark(f.targetPath, "destination", target)
	second := f.refresh()
	var left, right bytes.Buffer
	if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, second.ID, "base", "destination", &left); !errors.Is(err, hg.ErrNotFound) || left.Len() != 0 {
		t.Fatalf("diff left substituted target %q %v", left.String(), err)
	}
	if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, second.ID, "target", "destination", &right); err != nil || right.String() != "target\n" {
		t.Fatalf("target side %q %v", right.String(), err)
	}
	if second.TargetNode != hg.NodeID(target) || second.BaseNode != hg.NodeID(f.base) || second.ID == first.ID {
		t.Fatalf("target move not refreshed %+v", second)
	}
	f.run(f.sourcePath, "bookmark", "--delete", "proposal")
	failed, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
	if err == nil || failed.State == "ready" {
		t.Fatalf("deleted ref remained merge eligible %+v %v", failed, err)
	}
	p, err := f.service.GetPull(f.ctx, f.owner.Id, f.target.Id, f.pull.Number)
	if err != nil || p.CurrentComparisonID == second.ID {
		t.Fatalf("old current survives failed refresh %+v %v", p, err)
	}
	if _, _, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, first.ID); err != nil {
		t.Fatalf("immutable old comparison lost: %v", err)
	}
	f.targetNodeBookmark(f.sourcePath, "proposal", f.feature)
	branch, err := f.service.CreatePull(f.ctx, f.owner.Id, NewPull{SourceRepoID: f.source.Id, TargetRepoID: f.target.Id, Source: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBranch, Name: "default"}}, Target: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefNode, Name: target}}, Title: "Branch"})
	if err != nil {
		t.Fatal(err)
	}
	f.run(f.sourcePath, "pull", f.local)
	c, err := f.service.RefreshComparison(f.ctx, f.owner.Id, branch.ID)
	if !errors.Is(err, hg.ErrAmbiguousHead) || c.State == "ready" {
		t.Fatalf("ambiguous head accepted %+v %v", c, err)
	}
}

// Artifact possession, guessed IDs, and old comparisons never replace current
// account, endpoint or private-fork-ancestor authority.
func TestPullArtifactsRecheckVisibility(t *testing.T) {
	f := newComparisonFixture(t, true)
	ancestor := f.target
	// Both endpoints are descendants; ancestor ACL is independent of their grants.
	destination, err := f.service.Fork(f.ctx, f.owner.Id, ForkOptions{SourceID: ancestor.Id, OwnerID: f.owner.Id, Name: "destination"})
	if err != nil {
		t.Fatal(err)
	}
	f.pull, err = f.service.CreatePull(f.ctx, f.owner.Id, NewPull{SourceRepoID: f.source.Id, TargetRepoID: destination.Id, Source: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "proposal"}}, Target: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefNode, Name: f.base}}, Title: "Private comparison"})
	if err != nil {
		t.Fatal(err)
	}
	c := f.refresh()
	reader := &models.User{Name: "comparison-reader", Email: "reader@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reader); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{ancestor.Id, f.source.Id, destination.Id} {
		if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: id, Mode: models.READABLE}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := f.service.Comparison(f.ctx, reader.Id, f.pull.ID, c.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pull, id   int64
		side, path string
	}{{f.pull.ID + 1, c.ID, "source", "feature"}, {f.pull.ID, c.ID + 1, "source", "feature"}, {f.pull.ID, c.ID, "arbitrary", "feature"}, {f.pull.ID, c.ID, "source", "../feature"}, {f.pull.ID, c.ID, "source", ".hg/hgrc"}, {f.pull.ID, c.ID, "source", `a\b`}} {
		var b bytes.Buffer
		if err := f.service.ReadComparisonFile(f.ctx, reader.Id, tc.pull, tc.id, tc.side, tc.path, &b); err == nil || b.Len() != 0 {
			t.Fatalf("unsafe read %+v bytes=%d err=%v", tc, b.Len(), err)
		}
	}
	if err := models.DeleteAccess(&models.Access{UserId: reader.Id, RepoId: ancestor.Id}); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := f.service.ReadComparisonFile(f.ctx, reader.Id, f.pull.ID, c.ID, "source", "feature", &b); err == nil || b.Len() != 0 {
		t.Fatalf("private ancestor leaked %q %v", b.String(), err)
	}
	if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: ancestor.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Comparison(f.ctx, reader.Id, f.pull.ID, c.ID); err != nil {
		t.Fatalf("current reader denied %v", err)
	}
	if err := models.DeleteAccess(&models.Access{UserId: reader.Id, RepoId: f.source.Id}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Comparison(f.ctx, reader.Id, f.pull.ID, c.ID); err == nil {
		t.Fatal("revoked source ACL leaked old comparison")
	}
	f.owner.IsActive = false
	if err := models.UpdateUser(f.owner); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, c.ID); err == nil {
		t.Fatal("disabled owner leaked old comparison")
	}
}

// Bounded previews must never masquerade as full merge-ready comparisons;
// complete immutable snapshots still serve exact, permission-checked bytes.
func TestPullComparisonLargeBinaryAndStreaming(t *testing.T) {
	f := newComparisonFixture(t)
	for _, tc := range []struct {
		name, content string
		large, binary bool
	}{
		{"binary", "\x00binary\x01", false, true},
		{"long-line", strings.Repeat("x", hg.MaxDiffBytes+1024) + "\n", true, false},
		{"many-lines", strings.Repeat("line\n", hg.MaxDiffLines+100), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.write("feature", tc.content)
			node := f.commit(tc.name)
			f.run(f.sourcePath, "pull", f.local)
			f.targetNodeBookmark(f.sourcePath, "proposal", node)
			c, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
			if tc.large {
				if !errors.Is(err, hg.ErrComparisonTooLarge) || c.State != "unavailable" || !c.SnapshotComplete || c.Failure != "comparison-too-large" {
					t.Fatalf("large readiness %+v %v", c, err)
				}
			} else if err != nil || c.State != "ready" {
				t.Fatalf("binary readiness %+v %v", c, err)
			}
			_, review, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, c.ID)
			if tc.large {
				if !errors.Is(err, hg.ErrComparisonTooLarge) || !review.Diff.Truncated || len(review.Diff.Text) > hg.MaxDiffBytes || bytes.Count(review.Diff.Text, []byte("\n")) > hg.MaxDiffLines {
					t.Fatalf("unbounded preview bytes=%d truncated=%v err=%v", len(review.Diff.Text), review.Diff.Truncated, err)
				}
			} else if err != nil || !review.Diff.Binary {
				t.Fatalf("binary notice missing %+v %v", review.Diff, err)
			}
			same, againErr := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
			if same.ID != c.ID || tc.large && !errors.Is(againErr, hg.ErrComparisonTooLarge) {
				t.Fatalf("unchanged pins replaced version %+v %v", same, againErr)
			}
			var out bytes.Buffer
			if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, c.ID, "source", "feature", &out); err != nil || out.String() != tc.content {
				t.Fatalf("raw snapshot bytes=%d err=%v", out.Len(), err)
			}
			if tc.name == "long-line" {
				ctx, cancel := context.WithCancel(f.ctx)
				sink := &cancelComparisonWriter{cancel: cancel}
				err := f.service.ReadComparisonFile(ctx, f.owner.Id, f.pull.ID, c.ID, "source", "feature", sink)
				cancel()
				if err == nil || sink.n == 0 || sink.n >= len(tc.content) {
					t.Fatalf("stream ignored cancellation bytes=%d err=%v", sink.n, err)
				}
				sentinel := errors.New("writer refused")
				if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, c.ID, "source", "feature", errorComparisonWriter{sentinel}); !errors.Is(err, sentinel) {
					t.Fatalf("writer error lost: %v", err)
				}
			}
		})
	}
}

type cancelComparisonWriter struct {
	cancel context.CancelFunc
	n      int
}

func (w *cancelComparisonWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	w.cancel()
	return len(p), nil
}

type errorComparisonWriter struct{ err error }

func (w errorComparisonWriter) Write([]byte) (int, error) { return 0, w.err }

// Recovery must remove only positively owned interrupted native outputs and
// retain ready immutable history, unmarked collisions and unrelated artifacts.
func TestPullArtifactsInterruptedOwnedRecovery(t *testing.T) {
	f := newComparisonFixture(t)
	ready := f.refresh()
	reserve := func() models.PullComparison {
		t.Helper()
		c, err := models.ReservePullComparison(f.ctx, f.owner.Id, f.pull.ID)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	partial := reserve()
	if err := allocateComparison(f.artifacts, partial); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(f.artifacts, partial.ArtifactID, "snapshot")
	if err := os.Mkdir(partialPath, 0700); err != nil {
		t.Fatal(err)
	}
	f.run("", "clone", "--pull", "-U", f.targetPath, filepath.Join(partialPath, "repo"))
	orphan := reserve()
	if err := allocateComparison(f.artifacts, orphan); err != nil {
		t.Fatal(err)
	}
	source, target, err := comparisonEndpoints(f.ctx, f.host, f.pull)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.host.Runner().SnapshotReview(f.ctx, source, target, filepath.Join(f.artifacts, orphan.ArtifactID, "snapshot")); err != nil {
		t.Fatal(err)
	}
	collision := reserve()
	collisionPath := filepath.Join(f.artifacts, collision.ArtifactID)
	if err := os.Mkdir(collisionPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collisionPath, "keep"), []byte("not owned"), 0600); err != nil {
		t.Fatal(err)
	}
	unknown := filepath.Join(f.artifacts, "unrelated-prepared-operation")
	if err := os.Mkdir(unknown, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unknown, "prepared-review.json"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	// No concurrent operations: run the actual startup recovery boundary.
	if err := ReconcileComparisons(f.ctx, f.host); err != nil {
		t.Fatal(err)
	}
	for _, c := range []models.PullComparison{partial, orphan} {
		if _, err := os.Stat(filepath.Join(f.artifacts, c.ArtifactID)); !os.IsNotExist(err) {
			t.Fatalf("owned orphan retained %s %v", c.ArtifactID, err)
		}
		got, err := models.GetPullComparison(f.ctx, f.owner.Id, f.pull.ID, c.ID)
		if err != nil || got.State != "unavailable" || got.SnapshotComplete {
			t.Fatalf("pending comparison published %+v %v", got, err)
		}
	}
	for _, path := range []string{filepath.Join(collisionPath, "keep"), filepath.Join(unknown, "prepared-review.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("unowned data removed %s %v", path, err)
		}
	}
	if _, _, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, ready.ID); err != nil {
		t.Fatalf("ready history removed %v", err)
	}
	marker := filepath.Join(f.artifacts, ready.ArtifactID, "snapshot", "review-artifact.json")
	if err := os.WriteFile(marker, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, ready.ID, "source", "feature", &out); err == nil || out.Len() != 0 {
		t.Fatalf("corrupt marker streamed bytes=%d err=%v", out.Len(), err)
	}
	if err := ReconcileComparisons(f.ctx, f.host); err != nil {
		t.Fatal(err)
	}
	got, err := models.GetPullComparison(f.ctx, f.owner.Id, f.pull.ID, ready.ID)
	if err != nil || got.State != "unavailable" || got.SnapshotComplete {
		t.Fatalf("corrupt ready survived recovery %+v %v", got, err)
	}
	if err := ReconcileComparisons(f.ctx, f.host); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("corrupt unknown snapshot destroyed %v", err)
	}
}

func TestPullArtifactsRootAndOwnership(t *testing.T) {
	root := t.TempDir()
	private := filepath.Join(root, "private")
	public := filepath.Join(root, "public")
	if err := prepareComparisonRoot(private, public); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"relative", public, filepath.Join(public, "nested"), root} {
		if err := prepareComparisonRoot(bad, public); err == nil {
			t.Fatalf("unsafe root accepted %s", bad)
		}
	}
	c := models.PullComparison{ID: 1, PullID: 1, ArtifactID: strings.Repeat("a", 64), OwnershipNonce: strings.Repeat("b", 64)}
	path := filepath.Join(private, c.ArtifactID)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := allocateComparison(private, c); err == nil {
		t.Fatal("pre-existing allocation adopted")
	}
	if err := removeComparisonAllocation(private, c); err == nil {
		t.Fatal("unmarked allocation claimed")
	}
	if _, err := os.Stat(filepath.Join(path, "keep")); err != nil {
		t.Fatal(err)
	}
}

// A streaming grant is never reusable: revoke a real ACL after the first
// native cat chunk and prove later writes stop, even before the monitor ticks.
func TestPullArtifactsStreamRevocation(t *testing.T) {
	f := newComparisonFixture(t, true)
	f.write("feature", strings.Repeat("raw data\n", 400000))
	node := f.commit("large raw")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", node)
	c, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
	if !errors.Is(err, hg.ErrComparisonTooLarge) || !c.SnapshotComplete {
		t.Fatalf("snapshot %+v %v", c, err)
	}
	reader := &models.User{Name: "stream-reader", Email: "stream@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reader); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{f.source.Id, f.target.Id} {
		if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: id, Mode: models.READABLE}); err != nil {
			t.Fatal(err)
		}
	}
	sink := &revokeComparisonWriter{revoke: func() error { return models.DeleteAccess(&models.Access{UserId: reader.Id, RepoId: f.source.Id}) }}
	err = f.service.ReadComparisonFile(f.ctx, reader.Id, f.pull.ID, c.ID, "source", "feature", sink)
	if err == nil || sink.n == 0 || sink.writes != 1 {
		t.Fatalf("revocation failed bytes=%d writes=%d err=%v", sink.n, sink.writes, err)
	}
}

type revokeComparisonWriter struct {
	revoke    func() error
	n, writes int
}

func (w *revokeComparisonWriter) Write(p []byte) (int, error) {
	w.n += len(p)
	w.writes++
	if w.writes == 1 {
		if err := w.revoke(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func TestPullComparisonExcludesHiddenAndSecret(t *testing.T) {
	f := newComparisonFixture(t)
	f.write("secret", "secret data\n")
	f.run(f.local, "add", "secret")
	secret := f.commit("secret")
	f.run(f.sourcePath, "pull", f.local)
	f.run(f.sourcePath, "phase", "--force", "--secret", "-r", secret)
	f.run(f.local, "update", "-C", "-r", f.base)
	f.write("hidden", "hidden data\n")
	f.run(f.local, "add", "hidden")
	hidden := f.commit("hidden")
	f.run(f.sourcePath, "pull", f.local)
	f.run(f.sourcePath, "--config", "experimental.evolution=createmarkers", "debugobsolete", hidden)
	// A later pull from a publishing peer advances phases; restore the fixture
	// secret phase after all pulls before testing served-history exclusion.
	f.run(f.sourcePath, "phase", "--force", "--secret", "-r", secret)
	if got := f.run(f.sourcePath, "log", "--hidden", "-r", secret, "-T", "{phase}"); got != "secret" {
		t.Fatalf("fixture phase %s", got)
	}
	c := f.refresh()
	snapshot, err := comparisonSnapshot(f.artifacts, c)
	if err != nil {
		t.Fatal(err)
	}
	nodes := f.run(snapshot.Path, "log", "--hidden", "-r", "all()", "-T", "{node}\n")
	if strings.Contains(nodes, secret) || strings.Contains(nodes, hidden) {
		t.Fatalf("nonserved history retained %q", nodes)
	}
	for _, path := range []string{"secret", "hidden"} {
		var b bytes.Buffer
		if err := f.service.ReadComparisonFile(f.ctx, f.owner.Id, f.pull.ID, c.ID, "source", path, &b); err == nil || b.Len() != 0 {
			t.Fatalf("nonserved file leaked %s %v", path, err)
		}
	}
	f.targetNodeBookmark(f.sourcePath, "proposal", secret)
	failed, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
	if !errors.Is(err, hg.ErrNotFound) || failed.State == "ready" {
		t.Fatalf("secret selected %+v %v", failed, err)
	}
}

// Native criss-cross history has two maximal common ancestors. Never pick one
// arbitrarily and make the comparison merge-ready.
func TestPullComparisonRejectsMultipleBases(t *testing.T) {
	f := newComparisonFixture(t)
	left := f.feature
	f.run(f.local, "update", "-C", "-r", f.base)
	f.write("right", "right\n")
	f.run(f.local, "add", "right")
	right := f.commit("right")
	f.run(f.local, "merge", "--tool", ":merge", "-r", left)
	rightMerge := f.commit("right merge")
	f.run(f.local, "update", "-C", "-r", left)
	f.run(f.local, "merge", "--tool", ":merge", "-r", right)
	leftMerge := f.commit("left merge")
	f.run(f.sourcePath, "pull", "-r", leftMerge, f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", leftMerge)
	f.run(f.targetPath, "pull", "-r", rightMerge, f.local)
	f.targetNodeBookmark(f.targetPath, "destination", rightMerge)
	c, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
	if !errors.Is(err, hg.ErrAmbiguousHead) || c.State == "ready" || c.SnapshotComplete {
		t.Fatalf("ambiguous base accepted %+v %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(f.artifacts, c.ArtifactID)); !os.IsNotExist(err) {
		t.Fatalf("failed owned allocation retained %v", err)
	}
}

func TestPullArtifactsPreserveUnknownAndLinkedPaths(t *testing.T) {
	root := t.TempDir()
	c := models.PullComparison{ID: 1, PullID: 1, ArtifactID: strings.Repeat("c", 64), OwnershipNonce: strings.Repeat("d", 64)}
	if err := allocateComparison(root, c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, c.ArtifactID)
	unknown := filepath.Join(path, "unknown")
	if err := os.WriteFile(unknown, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeComparisonAllocation(root, c); err == nil {
		t.Fatal("unknown wrapper child removed")
	}
	if _, err := os.Stat(unknown); err != nil {
		t.Fatal(err)
	}
	// A native symlink test is skipped explicitly where Windows denies symlink
	// creation; ordinary-path proof must not be mislabeled junction/ACL proof.
	outside := t.TempDir()
	link := filepath.Join(root, "linked-root")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("native symlink unavailable: %v", err)
	}
	if err := prepareComparisonRoot(filepath.Join(link, "nested")); err == nil {
		t.Fatal("linked ancestor accepted")
	}
	if _, err := os.Stat(filepath.Join(outside, "nested")); !os.IsNotExist(err) {
		t.Fatalf("created through linked ancestor %v", err)
	}
}
