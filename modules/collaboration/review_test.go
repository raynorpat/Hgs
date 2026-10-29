package collaboration

import (
	"github.com/gogits/gogs/models"
	"strings"
	"testing"
)

func TestPullCommentAnchors(t *testing.T) {
	f := newComparisonFixture(t)
	c := f.refresh()
	for _, a := range []*InlineAnchor{{ComparisonID: c.ID, Path: "feature", Side: "source", Line: 1}, {ComparisonID: c.ID, Path: "base", Side: "base", Line: 1}} {
		if _, err := f.service.CommentPull(f.ctx, f.owner.Id, f.pull.ID, "**discussion**", a); err != nil {
			t.Fatal(err)
		}
	}
	for _, a := range []*InlineAnchor{{ComparisonID: c.ID, Path: "missing", Side: "source", Line: 1}, {ComparisonID: c.ID, Path: "feature", Side: "base", Line: 1}, {ComparisonID: c.ID, Path: "feature", Side: "target", Line: 1}, {ComparisonID: c.ID, Path: "feature", Side: "source", Line: 2}, {ComparisonID: c.ID, Path: "../feature", Side: "source", Line: 1}} {
		if _, err := f.service.CommentPull(f.ctx, f.owner.Id, f.pull.ID, "discussion", a); err == nil {
			t.Fatalf("invalid anchor accepted %+v", a)
		}
	}
	for _, body := range []string{" ", strings.Repeat("a", 65537)} {
		if _, err := f.service.CommentPull(f.ctx, f.owner.Id, f.pull.ID, body, nil); err == nil {
			t.Fatalf("invalid comment %q", body[:1])
		}
	}
	if _, err := f.service.CommentPull(f.ctx, f.owner.Id, f.pull.ID, strings.Repeat("a", 65536), nil); err != nil {
		t.Fatal(err)
	}
	f.write("feature", "second\n")
	node := f.commit("second")
	f.run(f.sourcePath, "pull", f.local)
	f.targetNodeBookmark(f.sourcePath, "proposal", node)
	f.refresh()
	comments, err := models.ListPullComments(f.ctx, f.owner.Id, f.pull.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != 3 || !comments[0].Outdated || comments[0].ComparisonID != c.ID {
		t.Fatalf("old anchors moved %+v", comments)
	}
	if err := f.service.ResolveThread(f.ctx, f.owner.Id, f.pull.ID, comments[0].ID, true); err != nil {
		t.Fatal(err)
	}
}
