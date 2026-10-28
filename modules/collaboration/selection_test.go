package collaboration

import (
	"github.com/gogits/gogs/modules/hg"
	"testing"
)

func TestPullStrategyInspection(t *testing.T) {
	f := newComparisonFixture(t)
	check := func(want hg.MergeStrategy) {
		t.Helper()
		c, e := f.service.RefreshComparison(f.ctx, f.owner.Id, f.pull.ID)
		if e != nil {
			t.Fatal(e)
		}
		strategy, e := f.service.ComparisonMergeStrategy(f.ctx, f.owner.Id, f.pull.ID, c.ID)
		if e != nil || strategy != want {
			t.Fatalf("strategy %s want %s: %v", strategy, want, e)
		}
	}
	check(hg.MergeFastForward)
	f.run(f.local, "branch", "cross-branch")
	f.write("feature", "cross branch\n")
	node := f.commit("cross branch")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", node)
	check(hg.MergeTwoParent)
	f.run(f.targetPath, "pull", f.sourcePath)
	f.targetNodeBookmark(f.targetPath, "destination", node)
	check(hg.MergeAlreadyIntegrated)
}
