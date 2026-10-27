package models

import (
	"context"
	"github.com/gogits/gogs/modules/setting"
	"strings"
	"testing"
)

func TestCatalogFiltersBeforeCountAndPage(t *testing.T) {
	newTestDatabase(t)
	old := setting.Service.RequireSignInView
	defer func() { setting.Service.RequireSignInView = old }()
	setting.Service.RequireSignInView = false
	admin := newAccessTestUser(t, "siteadmin", true)
	owner := newAccessTestUser(t, "owner", true)
	reader := newAccessTestUser(t, "reader", true)
	public := newAccessTestRepo(t, owner, "public", false)
	private := newAccessTestRepo(t, owner, "private", true)
	newAccessTestRepo(t, owner, "hidden", true)
	pending := newAccessTestRepo(t, owner, "pending", false)
	x.Id(pending.Id).Cols("state").Update(&Repository{State: RepositoryProvisioning})
	if err := AddAccess(&Access{UserId: reader.Id, RepoId: private.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}
	for _, visibility := range []string{"all", "public", "private", "bogus"} {
		p, err := ListAccessibleRepositories(context.Background(), 0, CatalogQuery{Visibility: visibility, PageSize: 1})
		if err != nil {
			t.Fatal(err)
		}
		if p.Total != 1 || len(p.Repositories) != 1 || p.Repositories[0].Id != public.Id {
			t.Fatalf("anonymous %s: %+v", visibility, p)
		}
	}
	p, err := ListAccessibleRepositories(context.Background(), reader.Id, CatalogQuery{Page: 2, PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 2 || len(p.Repositories) != 1 || p.Repositories[0].Id != public.Id {
		t.Fatalf("authorized count/page: %+v", p)
	}
	p, _ = ListAccessibleRepositories(context.Background(), admin.Id, CatalogQuery{Visibility: "private"})
	if p.Total != 0 {
		t.Fatal("site admin bypass")
	}
	p, _ = ListAccessibleRepositories(context.Background(), reader.Id, CatalogQuery{Text: "OWNER", Visibility: "private"})
	if p.Total != 1 {
		t.Fatalf("owner search: %+v", p)
	}
	p, _ = ListAccessibleRepositories(context.Background(), reader.Id, CatalogQuery{Scope: "mine"})
	if p.Total != 0 {
		t.Fatal("mine includes collaborator")
	}
	p, _ = ListAccessibleRepositories(context.Background(), owner.Id, CatalogQuery{OwnerID: owner.Id})
	if p.Total != 3 {
		t.Fatalf("owner count: %+v", p)
	}
	setting.Service.RequireSignInView = true
	p, _ = ListAccessibleRepositories(context.Background(), 0, CatalogQuery{})
	if p.Total != 0 || len(p.Repositories) != 0 {
		t.Fatal("sign-in policy leaked")
	}
	p, _ = ListAccessibleRepositories(context.Background(), reader.Id, CatalogQuery{})
	if p.Total != 2 {
		t.Fatal("signed discovery restricted")
	}
	if got := NormalizeCatalogQuery(CatalogQuery{Text: strings.Repeat("界", 201), Page: -1, PageSize: 999}); len([]rune(got.Text)) != 200 || got.Page != 1 || got.PageSize != 30 {
		t.Fatalf("limits: %+v", got)
	}
}

func TestCatalogCancellationAndPagingLimits(t *testing.T) {
	for _, size := range []int{-10, 0, 101, 99999} {
		if q := NormalizeCatalogQuery(CatalogQuery{PageSize: size}); q.PageSize != 30 {
			t.Fatalf("invalid size %d became %d", size, q.PageSize)
		}
	}
	if q := NormalizeCatalogQuery(CatalogQuery{PageSize: 100}); q.PageSize != 100 {
		t.Fatal("maximum page size rejected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ListAccessibleRepositories(ctx, 0, CatalogQuery{}); err != context.Canceled {
		t.Fatalf("cancelled catalog: %v", err)
	}
}
