package middleware

import (
	"github.com/gogits/gogs/modules/hg"
	"testing"
)

func TestProfileReadmeContentURLContract(t *testing.T) {
	// Invalid combinations fail before a repository lookup, even without a DB.
	for _, tc := range []struct {
		node hg.NodeID
		path string
		raw  bool
	}{{"", "README.md", false}, {"", "", true}, {"", "README.md", true}, {"tip", "README.md", false}} {
		if got, err := profileContentURL(1, tc.node, tc.path, tc.raw); err == nil || got != "" {
			t.Fatalf("accepted invalid URL request %+v: %q %v", tc, got, err)
		}
	}
}
