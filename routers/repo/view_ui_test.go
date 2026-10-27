package repo

import (
	"github.com/gogits/gogs/modules/hg"
	"testing"
)

func TestSharedUIRevisionHeadsKeepNodeIdentity(t *testing.T) {
	refs := []hg.Reference{{Kind: hg.RefBranch, Name: "shared / name", Node: hg.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, {Kind: hg.RefBranch, Name: "shared / name", Node: hg.NodeID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}, {Kind: hg.RefBookmark, Name: "shared / name", Node: hg.NodeID("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")}, {Kind: hg.RefTag, Name: "shared / name", Node: hg.NodeID("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")}}
	rows := referenceRows("/code/alice/lab", refs)
	if rows[0].SelectionURL != rows[0].URL || rows[1].SelectionURL != rows[1].URL || rows[0].SelectionURL == rows[1].SelectionURL {
		t.Fatal("named-branch head selection lost immutable node identity")
	}
	if rows[2].SelectionURL == rows[3].SelectionURL {
		t.Fatal("bookmark and tag sharing a name were conflated")
	}
}
