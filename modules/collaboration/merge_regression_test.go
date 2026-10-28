package collaboration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/hosting"
)

// These tests deliberately change policy after admission; admission checks
// alone must not authorize a prepared operation's eventual publication.
func TestPullMergePreparedPolicy(t *testing.T) {
	for _, policy := range []string{"declined", "privacy", "comparison"} {
		t.Run(policy, func(t *testing.T) {
			f := newComparisonFixture(t)
			c := f.refresh()
			defer func() { mergeFault = nil }()
			mergeFault = func(stage string) error {
				if stage != "prepared" {
					return nil
				}
				switch policy {
				case "declined":
					return models.SetPullRecordState(f.ctx, f.owner.Id, f.pull.ID, models.PullDeclined)
				case "privacy":
					f.source.IsPrivate = true
					return models.UpdateRepository(f.source)
				default:
					_, err := models.ReservePullComparison(f.ctx, f.owner.Id, f.pull.ID)
					return err
				}
			}
			res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "policy-changed"))
			if err == nil || res.State == "merged" {
				t.Fatalf("changed policy published: %+v %v", res, err)
			}
			if got := f.bookmarkNode(f.targetPath, "destination"); got != f.base {
				t.Fatalf("policy denial moved bookmark: %s", got)
			}
		})
	}
}

func (f *comparisonFixture) restart() {
	f.t.Helper()
	if err := f.host.Shutdown(f.ctx); err != nil {
		f.t.Fatal(err)
	}
	models.CloseEngine()
	if err := models.NewEngine(); err != nil {
		f.t.Fatal(err)
	}
	var err error
	f.host, err = hosting.New(f.ctx, hosting.Config{Mercurial: hg.Config{Executable: os.Getenv("GOGS_TEST_HG"), ConfigPath: filepath.Join(f.root, "config", "hgrc"), MaxProcesses: 1}, RepositoryRoot: filepath.Join(f.root, "repos"), ReviewArtifactRoot: f.artifacts, HostKeyPath: filepath.Join(f.root, "keys", "host"), PublicURL: "https://example.test"})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := ReconcileComparisons(f.ctx, f.host); err != nil {
		f.t.Fatal(err)
	}
	f.service = New(&pullProvider{host: f.host})
}

func TestPullMergeNativePartialRevocationAndRestart(t *testing.T) {
	f := newComparisonFixture(t)
	f.diverge("target-only")
	op, e := f.preparedOperation("partial-native")
	// The real application hook goes through this test relay to the original
	// controller with the original capability. Local bookmark publication opens
	// a second transaction (wire pushkey uses prepushkey). Revoke at that second
	// pretxnopen, after the authorized history transaction has committed.
	events := make(chan string, 16)
	var originalControl string
	transactions := 0
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		var hook hgbridge.HookRequest
		if err := json.Unmarshal(body, &hook); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		events <- hook.Hook
		if strings.HasPrefix(hook.Hook, "pretxnopen") {
			transactions++
		}
		if transactions == 2 {
			f.owner.IsActive = false
			if err := models.UpdateUser(f.owner); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "http://"+originalControl+r.URL.RequestURI(), bytes.NewReader(body))
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		req.Header = r.Header.Clone()
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body)
	}))
	defer relay.Close()
	err := f.host.WithRepositoryMutation(f.ctx, f.owner.Id, e, func(ctx context.Context, env map[string]string) error {
		originalControl = env[hgbridge.EnvControl]
		env[hgbridge.EnvControl] = strings.TrimPrefix(relay.URL, "http://")
		return f.host.Runner().PublishReview(ctx, f.publicationRequest(e), env)
	})
	if err == nil {
		t.Fatal("revoked bookmark transaction succeeded")
	}
	seenTransactions := 0
	for len(events) > 0 {
		event := <-events
		if strings.HasPrefix(event, "pretxnopen") {
			seenTransactions++
		}
	}
	if seenTransactions != 2 {
		t.Fatalf("real native callbacks absent: transactions=%d", seenTransactions)
	}
	st, err := models.ReviewPublicationStatus(op.ID)
	if err != nil || !st.Started || !st.Failed || st.Complete || st.Aborted {
		t.Fatalf("partial outcome: %+v %v", st, err)
	}
	if got := f.bookmarkNode(f.targetPath, "destination"); got != string(op.TargetNode) {
		t.Fatalf("revoked bookmark moved: %s", got)
	}
	if got := f.run(f.targetPath, "log", "-r", "merge()", "-T", "{node}"); got != string(op.ResultNode) {
		t.Fatalf("history result lost: %s", got)
	}
	f.restart()
	_ = f.service.ReconcileOperations(f.ctx)
	failed, err := models.LoadMergeOperation(op.ID)
	if err != nil || failed.State != models.MergeFailed || failed.ErrorCode != "publication-failed" || failed.Prepared != op.Prepared || failed.Expectation != op.Expectation || failed.ResultNode != op.ResultNode {
		t.Fatalf("partial provenance: %+v %v", failed, err)
	}
	if _, err := os.Stat(e.Prepared.Path); err != nil {
		t.Fatalf("partial artifact missing: %v", err)
	}
	f.owner.IsActive = true
	if err := models.UpdateUser(f.owner); err != nil {
		t.Fatal(err)
	}
	_, err = f.service.MergePull(f.ctx, f.owner.Id, MergeRequest{PullID: op.PullID, ComparisonID: op.ComparisonID, Source: op.SourceNode, Target: op.TargetNode, IdempotencyKey: "partial-after-regrant"})
	if !errors.Is(err, models.ErrMergePartial) {
		t.Fatalf("fresh key regenerated result after regrant: %v", err)
	}
	if got := f.run(f.targetPath, "log", "-r", "merge()", "-T", "{node}"); got != string(op.ResultNode) {
		t.Fatalf("duplicate result: %s", got)
	}
	if err := models.DeleteHgRepository(f.ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Prepared.Path); !os.IsNotExist(err) {
		t.Fatalf("terminal partial private payload retained: %v", err)
	}
	if err := f.service.ReconcileOperations(f.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.Prepared.Path); !os.IsNotExist(err) {
		t.Fatalf("recovery recreated deleted payload: %v", err)
	}
	denied, err := models.LoadMergeOperation(op.ID)
	if err != nil || denied.State != models.MergeFailed || denied.Prepared != op.Prepared || denied.Expectation != op.Expectation || denied.ResultNode != op.ResultNode {
		t.Fatalf("deleted-source partial denial lost provenance: %+v %v", denied, err)
	}
	if err := models.ValidateMergePublication(f.ctx, f.owner.Id, op.ID, f.source.Id, f.target.Id, op.SourceNode, op.TargetNode, op.ResultNode); err == nil {
		t.Fatal("partial binding became generic after deletion")
	}
}

func TestPullMergeRestartPublishedWithoutGrant(t *testing.T) {
	f := newComparisonFixture(t)
	f.diverge("restart-target")
	c := f.refresh()
	defer func() { mergeFault = nil }()
	stop := errors.New("lost publication response")
	mergeFault = func(stage string) error {
		if stage == "mutated" {
			return stop
		}
		return nil
	}
	res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "restart-published"))
	if !errors.Is(err, stop) || res.State != "recovering" {
		t.Fatalf("interruption: %+v %v", res, err)
	}
	mergeFault = nil
	op, err := models.LoadMergeOperation(res.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	f.owner.IsActive = false
	if err := models.UpdateUser(f.owner); err != nil {
		t.Fatal(err)
	}
	f.restart()
	if err := f.service.ReconcileOperations(f.ctx); err != nil {
		t.Fatal(err)
	}
	done, err := models.LoadMergeOperation(op.ID)
	if err != nil || done.State != models.MergeFinalized || done.ResultNode != op.ResultNode {
		t.Fatalf("published fact requires grant: %+v %v", done, err)
	}
	if got := f.run(f.targetPath, "log", "-r", "merge()", "-T", "{node}"); got != string(op.ResultNode) {
		t.Fatalf("restart regenerated result: %s", got)
	}
}

func TestPullMergeRestartPreparedReviewBlocks(t *testing.T) {
	f := newComparisonFixture(t)
	op, _ := f.preparedOperation("restart-prepared")
	reviewer := &models.User{Name: "late-reviewer", Email: "late@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reviewer); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: reviewer.Id, RepoId: f.target.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.AssignReviewers(f.ctx, f.owner.Id, f.pull.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReviewPull(f.ctx, reviewer.Id, f.pull.ID, op.ComparisonID, models.ReviewChangesRequested); err != nil {
		t.Fatal(err)
	}
	f.restart()
	if err := f.service.ReconcileOperations(f.ctx); err == nil {
		t.Fatal("recovered prepared operation ignored current review")
	}
	done, err := models.LoadMergeOperation(op.ID)
	if err != nil || done.State != models.MergeFailed {
		t.Fatalf("policy denial not terminal: %+v %v", done, err)
	}
	if got := f.bookmarkNode(f.targetPath, "destination"); got != f.base {
		t.Fatalf("recovered operation bypassed review: %s", got)
	}
}

func TestPullMergeCrossBranchDescendant(t *testing.T) {
	f := newComparisonFixture(t)
	f.run(f.local, "branch", "proposal-branch")
	f.write("branch-file", "branch\n")
	f.run(f.local, "add", "branch-file")
	feature := f.commit("cross branch")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", feature)
	c := f.refresh()
	res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "cross-branch-descendant"))
	if err != nil || res.State != "merged" {
		t.Fatalf("cross branch: %+v %v", res, err)
	}
	if got := f.run(f.targetPath, "log", "-r", string(res.Node), "-T", "{p1node} {p2node} {branch}"); got != f.base+" "+feature+" default" {
		t.Fatalf("lost parents/destination branch: %s", got)
	}
}

func (f *comparisonFixture) preparedOperation(key string) (models.MergeOperation, hosting.MutationExpectation) {
	f.t.Helper()
	c := f.refresh()
	stop := errors.New("prepared interruption")
	mergeFault = func(stage string) error {
		if stage == "prepared" {
			return stop
		}
		return nil
	}
	res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, key))
	mergeFault = nil
	if !errors.Is(err, stop) {
		f.t.Fatalf("prepare: %+v %v", res, err)
	}
	op, err := models.LoadMergeOperation(res.OperationID)
	if err != nil {
		f.t.Fatal(err)
	}
	var e hosting.MutationExpectation
	if err := json.Unmarshal([]byte(op.Expectation), &e); err != nil {
		f.t.Fatal(err)
	}
	return op, e
}

func (f *comparisonFixture) publicationRequest(e hosting.MutationExpectation) hg.PublishReviewRequest {
	return hg.PublishReviewRequest{Prepared: e.Prepared, TargetPath: f.targetPath, Expected: hg.ReviewEndpoint{Path: f.targetPath, Selection: e.Selection, Node: e.Node}, OperationID: e.OperationID}
}

func TestPullMergeNativePolicyBoundary(t *testing.T) {
	f := newComparisonFixture(t)
	op, e := f.preparedOperation("native-policy")
	err := f.host.WithRepositoryMutation(f.ctx, f.owner.Id, e, func(ctx context.Context, env map[string]string) error {
		if err := models.SetPullRecordState(ctx, f.owner.Id, f.pull.ID, models.PullDeclined); err != nil {
			return err
		}
		return f.host.Runner().PublishReview(ctx, f.publicationRequest(e), env)
	})
	if err == nil {
		t.Fatal("declined PR passed native authorization callback")
	}
	if got := f.bookmarkNode(f.targetPath, "destination"); got != f.base {
		t.Fatalf("native policy bypass: %s", got)
	}
	if got := f.run(f.targetPath, "log", "-T", "{node}"); got != f.base {
		t.Fatalf("denied native transaction imported history: %s", got)
	}
	_ = f.service.ReconcileOperations(f.ctx)
	current, err := models.LoadMergeOperation(op.ID)
	if err != nil || current.State != models.MergeFailed {
		t.Fatalf("native denial not settled: %+v %v", current, err)
	}
}

func TestPullMergeIndependentlyIntegrated(t *testing.T) {
	for _, equal := range []bool{false, true} {
		t.Run(map[bool]string{false: "ancestor", true: "equal"}[equal], func(t *testing.T) {
			f := newComparisonFixture(t)
			target := f.feature
			if !equal {
				f.write("after-feature", "independent\n")
				f.run(f.local, "add", "after-feature")
				target = f.commit("independent integration")
			}
			f.run(f.targetPath, "pull", f.local)
			f.targetNodeBookmark(f.targetPath, "destination", target)
			before := f.run(f.targetPath, "log", "-T", "{node}\n")
			c, err := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
			if err != nil {
				t.Fatalf("current integrated comparison unavailable: %+v %v", c, err)
			}
			if c.State != "ready" || c.Outcome != models.ComparisonIntegrated || c.BaseNode != c.SourceNode || !c.SnapshotComplete {
				t.Fatalf("missing typed native ancestry outcome: %+v", c)
			}
			stored, preview, err := f.service.Comparison(f.ctx, f.owner.Id, f.pull.ID, c.ID)
			if err != nil || stored.Outcome != models.ComparisonIntegrated || len(preview.Changesets) != 0 || len(preview.Diff.Text) != 0 {
				t.Fatalf("integrated comparison read fabricated changes or failed: %+v %+v %v", stored, preview, err)
			}
			res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "already-integrated"))
			if err != nil || res.State != "merged" || string(res.Node) != target {
				t.Fatalf("integrated outcome: %+v %v", res, err)
			}
			if got := f.run(f.targetPath, "log", "-T", "{node}\n"); got != before {
				t.Fatal("already integrated created history")
			}
		})
	}
}

func TestPullMergePrestartAbortSettles(t *testing.T) {
	f := newComparisonFixture(t)
	c := f.refresh()
	defer func() { mergeFault = nil }()
	lock := filepath.Join(f.targetPath, ".hg", "store", "journal")
	mergeFault = func(stage string) error {
		if stage == "prepared" {
			return os.WriteFile(lock, []byte("incomplete transaction"), 0600)
		}
		return nil
	}
	res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "abort-prestart"))
	if err == nil {
		t.Fatal("pre-start I/O fault succeeded")
	}
	st, statusErr := models.ReviewPublicationStatus(res.OperationID)
	if statusErr != nil || !st.Aborted || st.Started {
		t.Fatalf("expected authoritative nonstarted abort: %+v %v", st, statusErr)
	}
	if res.State != "failed" {
		t.Fatalf("aborted publication stranded active: %+v", res)
	}
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	mergeFault = nil
	res, err = f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "abort-new-key"))
	if err != nil || res.State != "merged" {
		t.Fatalf("abort did not release admission: %+v %v", res, err)
	}
}

func TestPullMergeTerminalPartialRetainsProvenance(t *testing.T) {
	f := newComparisonFixture(t)
	f.diverge("target-only")
	c := f.refresh()
	defer func() { mergeFault = nil }()
	stop := errors.New("stop prepared")
	mergeFault = func(stage string) error {
		if stage == "prepared" {
			return stop
		}
		return nil
	}
	res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "partial-original"))
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	op, err := models.LoadMergeOperation(res.OperationID)
	if err != nil {
		t.Fatal(err)
	}
	// Model terminal provenance is checked separately from the native partial
	// transaction tests: a failed publication is not proof no history imported.
	_, err = failMerge(f.host, op, "publication-failed", stop)
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(preparedMergeRoot(f.host), op.ID)); err != nil {
		t.Fatalf("partial artifact removed: %v", err)
	}
	mergeFault = nil
	if _, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "partial-fresh-key")); err == nil {
		t.Fatal("fresh key regenerated partial result")
	}
}

func TestPullMergePreparedOwnership(t *testing.T) {
	f := newComparisonFixture(t)
	id := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	path := filepath.Join(preparedMergeRoot(f.host), id)
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "unknown")
	if err := os.WriteFile(sentinel, []byte("not owned"), 0600); err != nil {
		t.Fatal(err)
	}
	removePreparedMerge(f.host, id)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("deleted unowned prepared directory: %v", err)
	}
	op := models.MergeOperation{ID: id, ArtifactID: id, PullID: f.pull.ID, ComparisonID: 1}
	if err := removeOwnedPrepared(f.host, op); err == nil {
		t.Fatal("DB-shaped identity adopted an unmarked directory")
	}
	if _, err := allocatePrepared(f.host, op); err == nil {
		t.Fatal("preparation adopted an existing unowned directory")
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("allocation deleted unowned storage: %v", err)
	}
}

func TestPullMergeStatusLookupFailureStaysRecovering(t *testing.T) {
	f := newComparisonFixture(t)
	op, e := f.preparedOperation("status-lookup-failure")
	// Disconnect persistence before the guard: neither its denied check nor
	// the failed status read establishes absence or an aborted publication.
	if err := models.CloseEngine(); err != nil {
		t.Fatal(err)
	}
	res, err := f.service.driveMerge(f.ctx, f.host, op)
	if err == nil || res.State != "recovering" {
		t.Fatalf("unknown status inferred terminal: %+v %v", res, err)
	}
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	stored, err := models.LoadMergeOperation(op.ID)
	if err != nil || stored.State != models.MergePrepared || stored.ResultNode != op.ResultNode {
		t.Fatalf("lost uncertain operation: %+v %v", stored, err)
	}
	if _, err := os.Stat(e.Prepared.Path); err != nil {
		t.Fatalf("uncertain artifact removed: %v", err)
	}
	if err := f.service.ReconcileOperations(f.ctx); err != nil {
		t.Fatal(err)
	}
	done, err := models.LoadMergeOperation(op.ID)
	if err != nil || done.State != models.MergeFinalized || done.ResultNode != op.ResultNode {
		t.Fatalf("uncertain retry regenerated: %+v %v", done, err)
	}
}
