package repo

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/middleware"
)

func TestRawPathIdentity(t *testing.T) {
	node := hg.NodeID(strings.Repeat("a", 40))
	for _, p := range []string{"A #?%.txt", "dir/%2f", "dir/é", string([]byte{'d', '/', 0xff})} {
		u := middleware.HgPathURL("/gogs/alice/repo", "raw", node, p)
		req := httptest.NewRequest("GET", u, nil)
		got, err := middleware.ParseHgPath(req.URL, "/gogs/alice/repo/raw/"+string(node))
		if err != nil || got != p {
			t.Fatalf("roundtrip %q via %s: %q %v", p, u, got, err)
		}
	}
	for _, suffix := range []string{"/%FF", "/%2f", "/%5c", "/..", "/.hg/store", "//x", "/x?path=eA", "?path=%%%", "?path=eA&path=eQ"} {
		req := httptest.NewRequest("GET", "/gogs/alice/repo/raw/"+string(node)+suffix, nil)
		if _, err := middleware.ParseHgPath(req.URL, "/gogs/alice/repo/raw/"+string(node)); err == nil {
			t.Errorf("accepted %s", suffix)
		}
	}
}

func TestMercurialViewCursor(t *testing.T) {
	q := hg.HistoryQuery{Snapshot: hg.NodeID(strings.Repeat("a", 40)), SnapshotRevision: 9, BeforeRevision: -1, Path: string([]byte{'a', 0xff})}
	token := encodeHistoryCursor(q)
	got, err := decodeHistoryCursor(token)
	if err != nil || got.Snapshot != q.Snapshot || got.SnapshotRevision != 9 || got.BeforeRevision != -1 || got.Path != q.Path {
		t.Fatalf("%+v %v", got, err)
	}
	for _, s := range []string{"bad", strings.Repeat("a", 9000), "eyJ2IjoyfQ"} {
		if _, err := decodeHistoryCursor(s); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
}
