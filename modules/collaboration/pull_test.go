package collaboration

import (
	"bytes"
	"context"
	"errors"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/setting"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
)

func TestPullCreateUnavailableAndReviewers(t *testing.T) {
	service := New(&testProvider{})
	if _, err := service.CreatePull(context.Background(), 1, NewPull{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable host: %v", err)
	}
	if err := service.SetPullState(context.Background(), 1, 1, models.PullMerged); !errors.Is(err, models.ErrPullState) {
		t.Fatalf("generic merged: %v", err)
	}
}

// TestPullCreateNativeEndpoints exercises the public service against real,
// disposable managed stores. Removing native resolution would accept the missing
// bookmark and equal-node cases and reopen a proposal with a deleted bookmark.
func TestPullCreateNativeEndpoints(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Skip("GOGS_TEST_HG required for native endpoint validation")
	}
	root := t.TempDir()
	oldDB, oldLog := models.DbCfg, setting.LogRootPath
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.ToSlash(filepath.Join(root, "test.db"))
	setting.LogRootPath = root
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { models.CloseEngine(); models.DbCfg = oldDB; setting.LogRootPath = oldLog })
	owner := &models.User{Name: "pull-native", Email: "pull-native@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(owner); err != nil {
		t.Fatal(err)
	}
	host, err := hosting.New(context.Background(), hosting.Config{Mercurial: hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "config", "hgrc")}, RepositoryRoot: filepath.Join(root, "repos"), HostKeyPath: filepath.Join(root, "keys", "host"), PublicURL: "https://example.test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	provider := &pullProvider{host: host}
	service := New(provider)
	ctx := context.Background()
	run := func(path string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := host.Runner().Run(ctx, path, args, nil, &out); err != nil {
			t.Fatalf("native %v: %v", args, err)
		}
		return strings.TrimSpace(out.String())
	}
	target, err := models.CreateHgRepository(ctx, host.Runner(), host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "target"})
	if err != nil {
		t.Fatal(err)
	}
	targetPath, _ := models.RepositoryStoragePath(host.RepositoryRoot(), target.Id)
	local := filepath.Join(root, "local")
	run("", "init", local)
	if err := os.WriteFile(filepath.Join(local, "base"), []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	run(local, "add", "base")
	run(local, "commit", "-u", "fixture", "-m", "base")
	base := run(local, "log", "-r", ".", "-T", "{node}")
	run(targetPath, "pull", local)
	source, err := service.Fork(ctx, owner.Id, ForkOptions{SourceID: target.Id, OwnerID: owner.Id, Name: "source"})
	if err != nil {
		t.Fatal(err)
	}
	sourcePath, _ := models.RepositoryStoragePath(host.RepositoryRoot(), source.Id)
	if err := os.WriteFile(filepath.Join(local, "feature"), []byte("feature"), 0600); err != nil {
		t.Fatal(err)
	}
	run(local, "add", "feature")
	run(local, "commit", "-u", "fixture", "-m", "feature")
	feature := run(local, "log", "-r", ".", "-T", "{node}")
	run(local, "bookmark", "proposal")
	run(sourcePath, "pull", local)
	input := NewPull{SourceRepoID: source.Id, TargetRepoID: target.Id, Source: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "proposal"}}, Target: hg.ReviewSelection{Ref: hg.RefSelection{Kind: hg.RefNode, Name: base}}, Title: "Native proposal"}
	p, err := service.CreatePull(ctx, owner.Id, input)
	if err != nil {
		t.Fatal(err)
	}
	if p.CurrentComparisonID != 0 || p.State != models.PullOpen {
		t.Fatalf("dishonest comparison state %+v", p)
	}
	got, err := service.GetPull(ctx, owner.Id, target.Id, p.Number)
	if err != nil || got.ID != p.ID {
		t.Fatalf("route %+v %v", got, err)
	}
	page, err := service.ListPulls(ctx, owner.Id, PullQuery{RepoID: target.Id})
	if err != nil || page.Total != 1 || page.PageSize != 30 {
		t.Fatalf("page %+v %v", page, err)
	}
	if err := service.EditPull(ctx, owner.Id, p.ID, "Edited native proposal", ""); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []hg.ReviewSelection{{Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "missing"}}, {Ref: hg.RefSelection{Kind: hg.RefNode, Name: base}}, {Ref: hg.RefSelection{Kind: hg.RefBookmark, Name: "proposal"}, Head: hg.NodeID(base)}, {Ref: hg.RefSelection{Kind: hg.RefNode, Name: "all()"}}} {
		q := input
		q.Source = selection
		if _, err := service.CreatePull(ctx, owner.Id, q); err == nil {
			t.Fatalf("invalid endpoint accepted %+v", selection)
		}
	}
	// Same repository, two valid distinct endpoints.
	same := input
	same.TargetRepoID = source.Id
	if _, err := service.CreatePull(ctx, owner.Id, same); err != nil {
		t.Fatalf("same repository: %v", err)
	}
	// A reader without source write cannot create, even for public endpoints.
	reader := &models.User{Name: "pull-reader", Email: "pull-reader@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(reader); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreatePull(ctx, reader.Id, input); err == nil {
		t.Fatal("read-only actor created")
	}
	if _, err := service.CreatePull(ctx, 0, input); err == nil {
		t.Fatal("anonymous actor created")
	}
	if err := service.SetPullState(ctx, owner.Id, p.ID, models.PullDeclined); err != nil {
		t.Fatal(err)
	}
	if err := service.SetPullState(ctx, owner.Id, p.ID, models.PullOpen); err != nil {
		t.Fatalf("reopen valid refs: %v", err)
	}
	run(sourcePath, "bookmark", "--delete", "proposal")
	if err := service.SetPullState(ctx, owner.Id, p.ID, models.PullDeclined); err != nil {
		t.Fatalf("cannot decline missing ref: %v", err)
	}
	if err := service.SetPullState(ctx, owner.Id, p.ID, models.PullOpen); err == nil {
		t.Fatal("reopened missing ref")
	}
	got, err = service.GetPull(ctx, owner.Id, target.Id, p.Number)
	if err != nil || got.State != models.PullDeclined {
		t.Fatalf("failed reopen changed state %+v %v", got, err)
	}
	targetNodes := run(targetPath, "log", "-T", "{node}\n")
	if strings.Contains(targetNodes, feature) || targetNodes != base {
		t.Fatalf("target history mutated %q", targetNodes)
	}
	// Releasing both read registrations is observable: shutdown must complete.
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := host.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("read registrations leaked: %v", err)
	}
	if _, err := service.CreatePull(ctx, owner.Id, input); err == nil {
		t.Fatal("shutdown admitted native read")
	}
	if provider.reads < 2 {
		t.Fatal("provider was not resolved lazily per operation")
	}
}

type pullProvider struct {
	host  *hosting.Service
	reads int
}

func (p *pullProvider) Service() *hosting.Service { p.reads++; return p.host }
