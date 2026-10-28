package repo

import (
	"github.com/gogits/gogs/modules/collaboration"
	"testing"
)

func TestPullOperationPresentation(t *testing.T) {
	for _, tc := range []struct {
		state, code, want string
		disabled          bool
	}{{"pending", "", "pending", true}, {"recovering", "", "recovering", true}, {"failed", "publication-failed", "failed", true}, {"failed", "conflict", "conflicted", true}, {"failed", "unsafe-checkout", "failed", true}, {"failed", "stale", "stale", true}, {"failed", "publication-aborted", "failed", false}, {"merged", "", "merged", true}} {
		status, disabled := pullOperationPresentation(collaboration.MergeResult{State: tc.state, Code: tc.code})
		if status != tc.want || disabled != tc.disabled {
			t.Fatalf("%+v: %s %v", tc, status, disabled)
		}
	}
}
