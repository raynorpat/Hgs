//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
)

// A real configured application accepts a native writer after PR preparation,
// then a new application process reconciles the durable prepared operation.
// Only preparation is fixture-driven: it uses the native snapshot and merge
// APIs, never fabricated PreparedMerge data or an alternate publication engine.
func TestPullMergeTransportWriterAndProcessRestart(t *testing.T) {
	for _, transport := range []string{transportHTTP, transportSSH} {
		t.Run(transport, func(t *testing.T) {
			a := newApplication(t, matrix(t)[0])
			a.stop()
			ctx := context.Background()
			artifacts := filepath.Join(a.root, "comparisons")
			config, err := os.ReadFile(a.config)
			if err != nil {
				t.Fatal(err)
			}
			config = []byte(strings.Replace(string(config), "[mercurial]", "[mercurial]\nREVIEW_ARTIFACT_ROOT = "+filepath.ToSlash(artifacts), 1))
			if err := os.WriteFile(a.config, config, 0600); err != nil {
				t.Fatal(err)
			}
			host, err := hosting.New(ctx, hosting.Config{Mercurial: hg.Config{Executable: hgPath(t), ConfigPath: filepath.Join(a.root, "preparation", "hgrc"), MaxProcesses: 1}, RepositoryRoot: a.repoRoot, ReviewArtifactRoot: artifacts, HostKeyPath: filepath.Join(a.root, "preparation-key"), PublicURL: "https://example.test"})
			if err != nil {
				t.Fatal(err)
			}
			defer host.Shutdown(ctx)
			if err := collaboration.ReconcileComparisons(ctx, host); err != nil {
				t.Fatal(err)
			}
			service := collaboration.New(uiProvider{s: host})
			store, _ := models.RepositoryStoragePath(a.repoRoot, a.repos["public"].Id)
			local := filepath.Join(a.root, "merge-client")
			a.inspect.must(t, "", "init", local)
			commit := func(file string) string {
				writeFile(t, filepath.Join(local, file), file)
				a.inspect.must(t, local, "add", file)
				a.inspect.must(t, local, "commit", "-u", "fixture", "-m", file)
				return a.inspect.stdout(t, local, "log", "-r", ".", "-T", "{node}")
			}
			base := commit("base")
			feature := commit("feature")
			a.inspect.must(t, store, "pull", local)
			a.inspect.must(t, store, "bookmark", "-r", base, "destination")
			a.inspect.must(t, store, "bookmark", "-r", feature, "proposal")
			src := hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "proposal"}}
			dst := hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "destination"}}
			owner := a.users["owner"].Id
			pull, err := service.CreatePull(ctx, owner, collaboration.NewPull{SourceRepoID: a.repos["public"].Id, TargetRepoID: a.repos["public"].Id, Source: src, Target: dst, Title: "Transport race"})
			if err != nil {
				t.Fatal(err)
			}
			comparison, err := service.RefreshComparison(ctx, owner, pull.ID)
			if err != nil {
				t.Fatal(err)
			}
			snapshot := hg.ReviewSnapshot{Path: filepath.Join(artifacts, comparison.ArtifactID, "snapshot", "repo"), Source: comparison.SourceNode, Target: comparison.TargetNode, Base: comparison.BaseNode}
			prepared, err := host.Runner().PrepareReviewMerge(ctx, snapshot, filepath.Join(a.root, "prepared-native"), "fixture", "merge")
			if err != nil {
				t.Fatal(err)
			}
			if err := host.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			// Start before storing the intent so startup cannot publish it yet.
			a.start(t)
			id := "11223344556677889900aabbccddeeff"
			op, err := models.BeginMergeOperation(ctx, owner, models.MergeAdmission{ID: id, IdempotencyKey: "transport-race", PullID: pull.ID, ComparisonID: comparison.ID, SourceNode: comparison.SourceNode, TargetNode: comparison.TargetNode})
			if err != nil {
				t.Fatal(err)
			}
			e := hosting.MutationExpectation{RepoID: pull.TargetRepoID, Selection: dst, Node: comparison.TargetNode, OperationID: op.ID, Source: hosting.MutationEndpoint{RepoID: pull.SourceRepoID, Selection: src, Node: comparison.SourceNode}, Prepared: prepared}
			preparedJSON, _ := json.Marshal(prepared)
			expectationJSON, _ := json.Marshal(e)
			if err := models.SetMergePrepared(op.ID, prepared.Result, prepared.Bookmark, string(preparedJSON), string(expectationJSON)); err != nil {
				t.Fatal(err)
			}
			a.inspect.must(t, local, "update", "-C", "-r", base)
			racer := commit("racing-writer")
			a.inspect.must(t, local, "bookmark", "-r", racer, "destination")
			remote, opts := a.remote(t, transport, "writer", "public")
			out, err := a.client.run(ctx, local, append(opts, "push", "--force", "--rev", racer, "--bookmark", "destination", remote)...)
			if !pushSucceeded(err, out) {
				t.Fatalf("native %s writer failed: %v %s", transport, err, out)
			}
			t.Logf("native %s racing writer moved destination to %s", transport, racer)
			a.stop()
			a.start(t)
			done, err := models.LoadMergeOperation(op.ID)
			if err != nil || done.State != models.MergeFailed || done.ErrorCode != "stale" {
				t.Fatalf("startup replayed stale PR: %+v %v", done, err)
			}
			publication, err := models.ReviewPublicationStatus(op.ID)
			if err != nil || !publication.Aborted || publication.Started {
				t.Fatalf("stale publication started: %+v %v", publication, err)
			}
			p, err := models.GetPullRecordByID(ctx, owner, pull.ID)
			if err != nil || p.State != models.PullOpen {
				t.Fatalf("false merged state: %+v %v", p, err)
			}
			if got := a.inspect.stdout(t, store, "log", "-r", "destination", "-T", "{node}"); got != racer {
				t.Fatalf("restart overwrote racing bookmark: %s", got)
			}
			if got := a.inspect.stdout(t, store, "log", "-r", "merge()", "-T", "{node}"); got != "" {
				t.Fatalf("stale PR imported merge: %s", got)
			}
		})
	}
}
