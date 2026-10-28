package collaboration

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
)

func (f *comparisonFixture) mergeRequest(c models.PullComparison, key string) MergeRequest {
	return MergeRequest{PullID: f.pull.ID, ComparisonID: c.ID, Source: c.SourceNode, Target: c.TargetNode, IdempotencyKey: key}
}

func (f *comparisonFixture) bookmarkNode(path, name string) string {
	f.t.Helper()
	return f.run(path, "log", "-r", name, "-T", "{node}")
}

func (f *comparisonFixture) pullState() models.PullState {
	f.t.Helper()
	p, err := models.GetPullRecordByID(f.ctx, f.owner.Id, f.pull.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return p.State
}

// diverge adds a target-only commit so the merge needs a two-parent changeset.
func (f *comparisonFixture) diverge(file string) string {
	f.t.Helper()
	f.run(f.local, "update", "-C", "-r", f.base)
	f.write(file, "target\n")
	f.run(f.local, "add", file)
	node := f.commit("target " + file)
	f.run(f.targetPath, "pull", "-r", node, f.local)
	f.targetNodeBookmark(f.targetPath, "destination", node)
	return node
}

func TestPullMergeRejectsStaleReview(t *testing.T) {
	f := newComparisonFixture(t)
	c := f.refresh()
	reviewer := &models.User{Name: "merge-reviewer", Email: "reviewer@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reviewer); err != nil {
		t.Fatal(err)
	}
	for _, a := range []*models.Access{{UserId: reviewer.Id, RepoId: f.target.Id, Mode: models.WRITABLE}, {UserId: reviewer.Id, RepoId: f.source.Id, Mode: models.READABLE}} {
		if err := models.AddAccess(a); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.service.AssignReviewers(f.ctx, f.owner.Id, f.pull.ID, []int64{reviewer.Id}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.ReviewPull(f.ctx, reviewer.Id, f.pull.ID, c.ID, models.ReviewChangesRequested); err != nil {
		t.Fatal(err)
	}
	if err := f.service.AssignReviewers(f.ctx, f.owner.Id, f.pull.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "blocked-merge")); !errors.Is(err, models.ErrMergeBlocked) {
		t.Fatalf("unassignment bypassed a live block: %v", err)
	}
	if err := models.DeleteAccess(&models.Access{UserId: reviewer.Id, RepoId: f.target.Id}); err != nil {
		t.Fatal(err)
	}
	// The reviewer lost target write, so the block is retired and no longer gates the merge.
	bad := f.mergeRequest(c, "stale-merge")
	bad.Target = hg.NodeID(strings.Repeat("a", 40))
	if _, err := f.service.MergePull(f.ctx, f.owner.Id, bad); !errors.Is(err, models.ErrPullConflict) {
		t.Fatalf("wrong pins accepted: %v", err)
	}
	reader := &models.User{Name: "merge-reader", Email: "reader@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reader); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: f.target.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: reader.Id, RepoId: f.source.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.MergePull(f.ctx, reader.Id, f.mergeRequest(c, "reader-merge")); err == nil {
		t.Fatal("reader merged")
	}
	f.diverge("moved")
	if _, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "moved-target")); !errors.Is(err, ErrMergeStale) {
		t.Fatalf("moved target merged from a stale comparison: %v", err)
	}
	if f.pullState() != models.PullOpen {
		t.Fatal("pull merged after refusal")
	}
}

func TestPullMergeKeepsHistoryAndBranch(t *testing.T) {
	t.Run("fast-forward", func(t *testing.T) {
		f := newComparisonFixture(t)
		c := f.refresh()
		f.run(f.targetPath, "bookmark", "untouched", "-r", f.base)
		res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "fast-forward"))
		if err != nil || res.State != "merged" || res.Node != hg.NodeID(f.feature) {
			t.Fatalf("fast-forward %+v %v", res, err)
		}
		if got := f.bookmarkNode(f.targetPath, "destination"); got != f.feature {
			t.Fatalf("bookmark %s", got)
		}
		if got := f.bookmarkNode(f.targetPath, "untouched"); got != f.base {
			t.Fatalf("unrelated bookmark moved to %s", got)
		}
		if f.pullState() != models.PullMerged {
			t.Fatal("pull not merged")
		}
		f.run(f.targetPath, "verify")
	})
	t.Run("two-parent", func(t *testing.T) {
		f := newComparisonFixture(t)
		target := f.diverge("destination-file")
		c := f.refresh()
		res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "two-parent"))
		if err != nil || res.State != "merged" {
			t.Fatalf("merge %+v %v", res, err)
		}
		parents := f.run(f.targetPath, "log", "-r", string(res.Node), "-T", "{p1node} {p2node}")
		if parents != target+" "+f.feature {
			t.Fatalf("parents %q", parents)
		}
		if got := f.bookmarkNode(f.targetPath, "destination"); got != string(res.Node) {
			t.Fatalf("bookmark %s", got)
		}
		f.run(f.targetPath, "verify")
	})
	t.Run("conflict", func(t *testing.T) {
		f := newComparisonFixture(t)
		f.run(f.local, "update", "-C", "-r", f.base)
		f.write("feature", "target version\n")
		f.run(f.local, "add", "feature")
		node := f.commit("target feature")
		f.run(f.targetPath, "pull", "-r", node, f.local)
		f.targetNodeBookmark(f.targetPath, "destination", node)
		c := f.refresh()
		res, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(c, "conflicting"))
		if !errors.Is(err, hg.ErrMergeConflict) || res.State != "failed" || res.Code != "conflict" {
			t.Fatalf("conflict %+v %v", res, err)
		}
		if got := f.bookmarkNode(f.targetPath, "destination"); got != node {
			t.Fatalf("conflict moved target to %s", got)
		}
		if f.pullState() != models.PullOpen {
			t.Fatal("conflicted pull marked merged")
		}
	})
}

// Every durable boundary is crashed in turn; recovery must publish one result
// and a repeated request must report that same operation and node.
func TestPullMergeIdempotencyAndRecovery(t *testing.T) {
	defer func() { mergeFault = nil }()
	for _, stage := range []string{"admitted", "prepared", "mutated", "published"} {
		t.Run(stage, func(t *testing.T) {
			f := newComparisonFixture(t)
			f.diverge("destination-file")
			c := f.refresh()
			req := f.mergeRequest(c, "recover-"+stage)
			crash := errors.New("injected crash after " + stage)
			mergeFault = func(s string) error {
				if s == stage {
					return crash
				}
				return nil
			}
			res, err := f.service.MergePull(f.ctx, f.owner.Id, req)
			if !errors.Is(err, crash) || res.State != "recovering" {
				t.Fatalf("crash %+v %v", res, err)
			}
			if f.pullState() != models.PullOpen {
				t.Fatal("pull merged before publication was confirmed")
			}
			other := req
			other.IdempotencyKey = "different-" + stage
			if _, err := f.service.MergePull(f.ctx, f.owner.Id, other); !errors.Is(err, models.ErrMergeActive) {
				t.Fatalf("second operation admitted: %v", err)
			}
			mismatch := req
			mismatch.Target = hg.NodeID(strings.Repeat("b", 40))
			if _, err := f.service.MergePull(f.ctx, f.owner.Id, mismatch); !errors.Is(err, models.ErrMergeIdempotency) {
				t.Fatalf("payload mismatch accepted: %v", err)
			}
			mergeFault = nil
			if err := f.service.ReconcileOperations(f.ctx); err != nil {
				t.Fatal(err)
			}
			done, err := f.service.MergeStatus(f.ctx, f.owner.Id, f.pull.ID, res.OperationID)
			if err != nil || done.State != "merged" || done.Node == "" {
				t.Fatalf("recovered %+v %v", done, err)
			}
			again, err := f.service.MergePull(f.ctx, f.owner.Id, req)
			if err != nil || again.OperationID != res.OperationID || again.Node != done.Node {
				t.Fatalf("repeat %+v %v", again, err)
			}
			if n := f.run(f.targetPath, "log", "-r", "merge()", "-T", "{node}\n"); n != string(done.Node) {
				t.Fatalf("merge nodes %q, want only %s", n, done.Node)
			}
			if f.pullState() != models.PullMerged {
				t.Fatal("pull not merged after recovery")
			}
		})
	}
}

// A native push that moves the target between preparation and publication must
// fail the operation as stale instead of replaying against the new endpoint.
func TestPullMergeConcurrentWriters(t *testing.T) {
	defer func() { mergeFault = nil }()
	f := newComparisonFixture(t)
	c := f.refresh()
	req := f.mergeRequest(c, "racing-writer")
	mergeFault = func(stage string) error {
		if stage == "prepared" {
			f.diverge("racing-push")
		}
		return nil
	}
	res, err := f.service.MergePull(f.ctx, f.owner.Id, req)
	if !errors.Is(err, ErrMergeStale) || res.State != "failed" || res.Code != "stale" {
		t.Fatalf("race %+v %v", res, err)
	}
	if f.pullState() != models.PullOpen {
		t.Fatal("stale merge marked merged")
	}
	if got := f.run(f.targetPath, "log", "-r", "merge()", "-T", "{node}"); got != "" {
		t.Fatalf("stale merge published %s", got)
	}
	mergeFault = nil
	var wg sync.WaitGroup
	next := f.refresh()
	results := make([]error, 2)
	nodes := make([]hg.NodeID, 2)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, err := f.service.MergePull(f.ctx, f.owner.Id, f.mergeRequest(next, "same-key-twice"))
			results[i], nodes[i] = err, r.Node
		}(i)
	}
	wg.Wait()
	merged := 0
	for i, err := range results {
		if err == nil && nodes[i] != "" {
			merged++
		} else if !errors.Is(err, ErrMergeBusy) && !errors.Is(err, models.ErrPullState) && !errors.Is(err, models.ErrPullConflict) {
			t.Fatalf("racer %d: %v", i, err)
		}
	}
	if merged == 0 || f.pullState() != models.PullMerged {
		t.Fatalf("no racer merged: %v", results)
	}
	if n := strings.Fields(f.run(f.targetPath, "log", "-r", "merge()", "-T", "{node}\n")); len(n) != 1 {
		t.Fatalf("expected one merge node, got %v", n)
	}
}
