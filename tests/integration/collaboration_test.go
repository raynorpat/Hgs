//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
)

func TestCollaborationTransportDeletionRestart(t *testing.T) {
	a := newConfiguredApplication(t, matrix(t)[0], func(config string) string {
		return strings.Replace(config, "MAX_PROCESSES = 2", "REVIEW_ARTIFACT_ROOT = "+filepath.ToSlash(filepath.Join(filepath.Dir(models.DbCfg.Path), "comparisons"))+"\nMAX_PROCESSES = 2", 1)
	}, 5*time.Minute)
	ctx := context.Background()
	for _, transport := range []string{transportHTTP, transportSSH} {
		remote, opts := a.remote(t, transport, "reader", "private")
		a.client.must(t, "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "before-"+transport))...)
	}
	// Deliberately stop at the durable tombstone. No native storage is removed
	// while another process owns the real hosting registry.
	interrupted := errors.New("simulated drain interruption")
	if err := models.DeleteHgRepository(ctx, a.repoRoot, a.repos["private"].Id, func(context.Context, int64) error { return interrupted }); err == nil || !strings.Contains(err.Error(), interrupted.Error()) {
		t.Fatalf("tombstone interruption: %v", err)
	}
	for _, transport := range []string{transportHTTP, transportSSH} {
		remote, opts := a.remote(t, transport, "reader", "private")
		if out, err := a.client.run(ctx, "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "denied-"+transport))...); err == nil {
			t.Fatalf("deleting repository cloned over %s: %s", transport, out)
		}
	}
	a.stop()
	a.start(t)
	if _, err := models.GetRepositoryById(a.repos["private"].Id); !errors.Is(err, models.ErrRepoNotExist) {
		t.Fatalf("startup did not finalize deleting repository: %v", err)
	}
	for _, transport := range []string{transportHTTP, transportSSH} {
		remote, opts := a.remote(t, transport, "reader", "private")
		if out, err := a.client.run(ctx, "", append(append([]string{}, opts...), "clone", remote, filepath.Join(a.root, "restarted-"+transport))...); err == nil {
			t.Fatalf("restart recreated deleted repository over %s: %s", transport, out)
		}
	}
}

func TestCollaborationRevocationEverySurface(t *testing.T) {
	f := seededPullHTTP(t)
	p := f.create(t)
	ctx := context.Background()
	c, err := f.service.RefreshComparison(ctx, f.owner.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := models.RecordRepositoryVisit(ctx, f.reader.Id, f.source.Id, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.source.IsPrivate = true
	if err := models.UpdateRepository(f.source); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{f.route + "/1", fmt.Sprintf("%s/1/comparisons/%d/files/proposal.txt?side=source", f.route, c.ID)} {
		w := f.get(t, path, "reader")
		if w.Code != 404 || strings.Contains(w.Body.String(), "proposal changes") {
			t.Fatalf("revoked route %s leaked: %d", path, w.Code)
		}
	}
	page, err := models.ListPullRecords(ctx, f.reader.Id, models.PullQuery{RepoID: f.public.Id, State: "all"})
	if err != nil || page.Total != 0 {
		t.Fatalf("revoked PR count: %+v %v", page, err)
	}
	recents, err := models.RecentRepositories(ctx, f.reader.Id)
	if err != nil {
		t.Fatal(err)
	}
	for _, repo := range recents {
		if repo.Id == f.source.Id {
			t.Fatal("revoked recent leaked")
		}
	}
	pageRepos, err := models.ListAccessibleRepositories(ctx, f.reader.Id, models.CatalogQuery{Text: f.source.Name})
	if err != nil || pageRepos.Total != 0 {
		t.Fatalf("revoked catalog: %+v %v", pageRepos, err)
	}
	// Actual deletion permits only a sanitized target-authorized tombstone.
	if err := models.DeleteHgRepository(ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
	w := f.get(t, f.route+"/1", "reader")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Deleted source") {
		t.Fatalf("tombstone: %d %s", w.Code, w.Body.String())
	}
	for _, secret := range []string{"Actual fork proposal", "Safe description", "proposal-fork", "proposal changes", "/comments", "/refresh"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("tombstone leaked %q", secret)
		}
	}
	w = f.post(t, f.route+"/1/comments", "owner", url.Values{"content": {"resurrect"}}, true)
	if w.Code == 303 {
		t.Fatal("tombstone granted a new write")
	}
}

func TestCollaborationMergedHistoryHTTP(t *testing.T) {
	f := seededPullHTTP(t)
	p := f.create(t)
	ctx := context.Background()
	c, err := f.service.RefreshComparison(ctx, f.owner.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.MergePull(ctx, f.owner.Id, collaboration.MergeRequest{PullID: p.ID, ComparisonID: c.ID, Source: c.SourceNode, Target: c.TargetNode, IdempotencyKey: "retained-http"}); err != nil {
		t.Fatal(err)
	}
	if err := models.DeleteHgRepository(ctx, f.host.RepositoryRoot(), f.source.Id, f.host.DrainRepository, f.service.AfterRepositoryDeleted); err != nil {
		t.Fatal(err)
	}
	w := f.get(t, f.route+"/1", "reader")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Actual fork proposal") || !strings.Contains(w.Body.String(), "Deleted source") {
		t.Fatalf("retained merged detail: %d %s", w.Code, w.Body.String())
	}
	for _, mutation := range []string{"action=\"/code" + f.route + "/1/comments", "action=\"/code" + f.route + "/1/refresh", "action=\"/code" + f.route + "/1/edit"} {
		if strings.Contains(w.Body.String(), mutation) {
			t.Fatalf("retained view offers write: %s", mutation)
		}
	}
	w = f.get(t, fmt.Sprintf("%s/1/comparisons/%d/files/proposal.txt?side=source", f.route, c.ID), "reader")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "line one") {
		t.Fatalf("accepted file: %d %s", w.Code, w.Body.String())
	}
}
