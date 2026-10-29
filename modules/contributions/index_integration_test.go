//go:build integration

package contributions

import (
	"bytes"
	"context"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/hg"
	"github.com/gogits/gogs/modules/hosting"
	"github.com/gogits/gogs/modules/setting"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestContributionVisibleHistoryNative(t *testing.T) {
	exe := os.Getenv("GOGS_TEST_HG")
	if exe == "" {
		t.Skip("GOGS_TEST_HG is not configured; native history unverified")
	}
	ctx := context.Background()
	root := t.TempDir()
	oldDB, oldLog := models.DbCfg, setting.LogRootPath
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.Join(root, "index.db")
	setting.LogRootPath = root
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { models.CloseEngine(); models.DbCfg = oldDB; setting.LogRootPath = oldLog })
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	host, err := hosting.New(ctx, hosting.Config{Mercurial: hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "config", "hgrc"), MaxProcesses: 2}, RepositoryRoot: filepath.Join(root, "repos"), HostKeyPath: filepath.Join(root, "hostkey"), PublicURL: "https://example.invalid"})
	if err != nil {
		t.Fatal(err)
	}
	defer host.Shutdown(ctx)
	u := &models.User{Name: "indexer", Email: "indexer@example.invalid", Passwd: "fixture-password", IsActive: true}
	if err = models.CreateUser(u); err != nil {
		t.Fatal(err)
	}
	repo, err := models.CreateHgRepository(ctx, host.Runner(), host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: u.Id, ActorID: u.Id, Name: "all-heads"})
	if err != nil {
		t.Fatal(err)
	}
	path, err := models.ReadyRepositoryStorage(host.RepositoryRoot(), repo.Id)
	if err != nil {
		t.Fatal(err)
	}
	run := func(p string, args ...string) string {
		t.Helper()
		var out bytes.Buffer
		if err := host.Runner().Run(ctx, p, args, nil, &out); err != nil {
			t.Fatalf("native %v: %v", args, err)
		}
		return out.String()
	}
	run(path, "debugbuilddag", "+101 :base @feature . :feature <base @default . /feature :merge <base @side . <merge @secret .")
	run(path, "phase", "--force", "--secret", "-r", "tip")
	paths := 0
	index := NewService(host.Runner(), func(ctx context.Context, id int64) (string, error) {
		paths++
		if paths == 2 {
			// A confirmed fixture publication after the pinned scan must stay dirty.
			run(path, "debugbuilddag", "--from-existing", "$ @later .")
			if err := models.EnqueueContribution(ctx, "repository", repo.Id); err != nil {
				return "", err
			}
		}
		return models.ReadyRepositoryStorage(host.RepositoryRoot(), id)
	}, nil, time.Now)
	if err = index.SetMaintenanceRead(host.WithMaintenanceRead); err != nil {
		t.Fatal(err)
	}
	jobs, err := models.PendingContributionJobs(ctx, 0, 100)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("creation job: %+v %v", jobs, err)
	}
	started := time.Now()
	if err = index.IndexRepository(ctx, repo.Id); err != nil {
		t.Fatal(err)
	}
	count := func() int64 {
		t.Helper()
		n, err := db.Where("repository_id = ? AND generation > 0", repo.Id).Count(new(models.ContributionRepository))
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if n := count(); n != 105 {
		t.Fatalf("fixed all-head snapshot expected 105 visible nodes, got %d", n)
	}
	if err = models.CompleteContributionJob(ctx, jobs[0].ID, jobs[0].Requested); err != nil {
		t.Fatal(err)
	}
	jobs, err = models.PendingContributionJobs(ctx, 0, 100)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("racing publication lost: %+v %v", jobs, err)
	}
	if err = index.IndexRepository(ctx, repo.Id); err != nil {
		t.Fatal(err)
	}
	if n := count(); n != 106 {
		t.Fatalf("later publication missing: %d", n)
	}
	var events []models.ContributionEvent
	if err = db.Where("kind = ?", "changeset").Find(&events); err != nil || len(events) != 106 {
		t.Fatalf("dedup: %d %v", len(events), err)
	}
	for _, event := range events {
		if event.ActorID != 0 {
			t.Fatal("unverified native author attributed")
		}
	}
	fork, err := models.CreateHgRepository(ctx, host.Runner(), host.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: u.Id, ActorID: u.Id, Name: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	forkPath, err := models.ReadyRepositoryStorage(host.RepositoryRoot(), fork.Id)
	if err != nil {
		t.Fatal(err)
	}
	run(forkPath, "pull", path)
	if err = index.IndexRepository(ctx, fork.Id); err != nil {
		t.Fatal(err)
	}
	n, err := db.Where("kind = ?", "changeset").Count(new(models.ContributionEvent))
	if err != nil || n != 106 {
		t.Fatalf("fork duplicated full-node events: %d %v", n, err)
	}
	if err = index.IndexRepository(ctx, repo.Id); err != nil {
		t.Fatal(err)
	}
	t.Logf("106 visible nodes, all named heads and merge, secret exclusion, 100-entry page cap, concurrent snapshot/dirty replay and fork dedup in %s", time.Since(started))
}
