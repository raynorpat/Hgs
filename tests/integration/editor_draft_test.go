//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/editordraft"
)

func TestEditorDraftOwnerAndPermission(t *testing.T) {
	f := accountApp(t, true)
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := &models.Repository{OwnerId: f.admin.Id, Name: "draft-private", LowerName: "draft-private", IsPrivate: true, State: models.RepositoryReady}
	if _, err = db.Insert(r); err != nil {
		t.Fatal(err)
	}
	s := editordraft.NewService(models.NewEditorDraftStore(), func(ctx context.Context, a int64, target editordraft.Target) error {
		return models.AuthorizeEditorDraft(ctx, a, target.RepositoryID, target.Kind, target.ItemID)
	}, time.Now)
	ctx := context.Background()
	g, err := s.Open(ctx, f.admin.Id, "collection", editordraft.Target{RepositoryID: r.Id, Kind: "issue_new"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, f.admin.Id, "collection", g.PageID, g.PageSecret, 0, editordraft.Payload{Body: "private text"}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Restore(ctx, f.member.Id, "collection", g.PageID, g.PageSecret); !errors.Is(err, editordraft.ErrForbidden) {
		t.Fatalf("cross-account recovery: %v", err)
	}
	source := &models.Repository{OwnerId: f.admin.Id, Name: "unrelated-source", LowerName: "unrelated-source", IsPrivate: true, State: models.RepositoryReady}
	if _, err = db.Insert(source); err != nil {
		t.Fatal(err)
	}
	pg, err := s.Open(ctx, f.admin.Id, "collection", editordraft.Target{RepositoryID: r.Id, Kind: "pull_new"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Save(ctx, f.admin.Id, "collection", pg.PageID, pg.PageSecret, 0, editordraft.Payload{Metadata: map[string]string{"source_repository": f.admin.Name + "/unrelated-source"}}); !errors.Is(err, editordraft.ErrInvalidPayload) {
		t.Fatalf("unrelated source became recoverable: %v", err)
	}
	if snap, err := s.Restore(ctx, f.admin.Id, "collection", pg.PageID, pg.PageSecret); err != nil || snap.Version != 0 || len(snap.Payload.Metadata) != 0 {
		t.Fatalf("candidate denial wrote a recovery payload: %+v %v", snap, err)
	}
	if _, err = db.Id(r.Id).Cols("state").Update(&models.Repository{State: models.RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Restore(ctx, f.admin.Id, "collection", g.PageID, g.PageSecret); !errors.Is(err, editordraft.ErrForbidden) {
		t.Fatalf("removed target recovery: %v", err)
	}
}

func TestEditorDraftBrowserLifecycle(t *testing.T) {
	t.Skip("Real-browser lifecycle evidence is controller-owned; Go tests do not prove browser timing, cookies, session reauthentication, or bfcache behavior")
}
