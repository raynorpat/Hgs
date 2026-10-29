//go:build integration

package integration

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
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

// Run a real configured application with a private package tree and a server
// PATH whose directories contain no Git executable. Native HTTPS clients keep
// their own environment; every collaboration mutation uses production forms.
func TestCollaborationAcceptanceNoGit(t *testing.T) {
	packageRoot := t.TempDir()
	binary := filepath.Join(packageRoot, filepath.Base(gogsExe))
	data, err := os.ReadFile(gogsExe)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, data, 0700); err != nil {
		t.Fatal(err)
	}
	for _, tree := range []string{"conf", "public", "templates"} {
		if err = os.CopyFS(filepath.Join(packageRoot, tree), os.DirFS(filepath.Join("../..", tree))); err != nil {
			t.Fatal(err)
		}
	}
	var safe []string
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		forbidden := false
		for _, name := range []string{"git", "git.exe", "git.cmd", "git.bat"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				forbidden = true
			}
		}
		if !forbidden {
			safe = append(safe, dir)
		}
	}
	originalBinary := gogsExe
	gogsExe = binary
	defer func() { gogsExe = originalBinary }()
	t.Setenv("GOGS_TEST_BINARY", binary)
	t.Setenv("GOGS_TEST_ASSETS", packageRoot)
	t.Setenv("GOGS_TEST_PACKAGE_DIRECT", "1")
	t.Setenv("GOGS_TEST_SERVER_PATH", strings.Join(safe, string(os.PathListSeparator)))
	t.Logf("actual %s application with Git-free server PATH=%s", runtime.GOOS, os.Getenv("GOGS_TEST_SERVER_PATH"))
	a := newConfiguredApplication(t, matrix(t)[0], func(config string) string {
		return strings.Replace(config, "SESSION_TIMEOUT = 2", "SESSION_TIMEOUT = 120", 1)
	}, 5*time.Minute)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	token := ""
	request := func(method, path string, form url.Values) (int, string, string) {
		t.Helper()
		body := ""
		if form != nil {
			body = form.Encode()
		}
		req, e := http.NewRequest(method, "https://"+a.address+path, strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Accept", "text/html")
		if method == "POST" {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("X-Csrf-Token", token)
		}
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer response.Body.Close()
		b, e := io.ReadAll(response.Body)
		if e != nil {
			t.Fatal(e)
		}
		if match := regexp.MustCompile(`<meta name="_csrf" content="([^"]+)"`).FindSubmatch(b); match != nil {
			token = string(match[1])
		}
		return response.StatusCode, string(b), response.Header.Get("Location")
	}
	check := func(method, path string, form url.Values, want int) (string, string) {
		t.Helper()
		code, body, location := request(method, path, form)
		if code != want {
			t.Fatalf("%s %s=%d want %d: %s", method, path, code, want, body)
		}
		return body, location
	}
	check("GET", "/user/login", nil, 200)
	check("POST", "/user/login", url.Values{"uname": {"owner"}, "password": {"password"}}, 302)
	target, opts := a.remote(t, transportHTTP, "owner", "public")
	work := filepath.Join(a.root, "native-proposal")
	a.client.must(t, "", append(append([]string{}, opts...), "clone", target, work)...)
	if err = os.WriteFile(filepath.Join(work, "base.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a.client.must(t, work, "add")
	a.client.must(t, work, "commit", "-u", "owner", "-m", "base")
	a.client.must(t, work, append(append([]string{}, opts...), "push", target)...)
	// The fork has an exclusive source fence. Wait for this fixture's canonical
	// post-push index job to finish before the single mutation POST.
	baseNode := strings.TrimSpace(a.client.stdout(t, work, "log", "-r", "tip", "-T", "{node}"))
	waitCollaborationIndex(t, a, a.repos["public"].Id, baseNode)
	check("GET", "/owner/public/fork", nil, 200)
	_, progress := check("POST", "/owner/public/fork", url.Values{"uid": {fmt.Sprint(a.users["owner"].Id)}, "repo_name": {"acceptance-fork"}}, 302)
	for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		code, body, _ := request("GET", progress+"/status", nil)
		if code == 200 && strings.Contains(body, `"ready"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fork timeout: %d %s", code, body)
		}
	}
	source := strings.Replace(target, "/public", "/acceptance-fork", 1)
	if err = os.WriteFile(filepath.Join(work, "proposal.txt"), []byte("proposal\n"), 0600); err != nil {
		t.Fatal(err)
	}
	a.client.must(t, work, "add")
	a.client.must(t, work, "commit", "-u", "owner", "-m", "proposal")
	a.client.must(t, work, append(append([]string{}, opts...), "push", source)...)
	sourceNode := strings.TrimSpace(a.client.stdout(t, work, "log", "-r", "tip", "-T", "{node}"))
	check("GET", "/owner/public/pulls/new", nil, 200)
	_, detail := check("POST", "/owner/public/pulls/new", url.Values{"title": {"No Git acceptance"}, "source_repository": {"owner/acceptance-fork"}, "source_kind": {"branch"}, "source_name": {"default"}, "target_kind": {"branch"}, "target_name": {"default"}}, 303)
	check("GET", detail, nil, 200)
	check("POST", detail+"/refresh", url.Values{}, 303)
	body, _ := check("GET", detail, nil, 200)
	comparison := hiddenPull(t, body, "comparison_id")
	check("POST", detail+"/reviewers", url.Values{"reviewers": {"writer"}}, 303)
	check("POST", "/user/logout", nil, 302)
	check("GET", "/user/login", nil, 200)
	check("POST", "/user/login", url.Values{"uname": {"writer"}, "password": {"password"}}, 302)
	check("GET", detail, nil, 200)
	check("POST", detail+"/review", url.Values{"comparison_id": {comparison}, "state": {"approved"}}, 303)
	check("POST", "/user/logout", nil, 302)
	check("GET", "/user/login", nil, 200)
	check("POST", "/user/login", url.Values{"uname": {"owner"}, "password": {"password"}}, 302)
	body, _ = check("GET", detail, nil, 200)
	_, operation := check("POST", detail+"/merge", url.Values{"confirm": {"true"}, "comparison_id": {comparison}, "source_node": {hiddenPull(t, body, "source_node")}, "target_node": {hiddenPull(t, body, "target_node")}, "merge_key": {hiddenPull(t, body, "merge_key")}}, 303)
	for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(250 * time.Millisecond) {
		req, e := http.NewRequest("GET", "https://"+a.address+operation, nil)
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Accept", "application/json")
		response, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		payload, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil {
			t.Fatal(e)
		}
		var status struct{ State, Code string }
		if e = json.Unmarshal(payload, &status); e != nil {
			t.Fatalf("operation JSON: %s %v", payload, e)
		}
		if response.StatusCode == 200 && status.State == "merged" {
			break
		}
		if status.State == "failed" || time.Now().After(deadline) {
			serverLog, _ := os.ReadFile(filepath.Join(a.root, "server.log"))
			if len(serverLog) > 12000 {
				serverLog = serverLog[len(serverLog)-12000:]
			}
			t.Fatalf("same operation %s: HTTP%d %+v; server tail:\n%s", operation, response.StatusCode, status, serverLog)
		}
	}
	store, _ := models.RepositoryStoragePath(a.repoRoot, a.repos["public"].Id)
	if got := strings.TrimSpace(a.inspect.stdout(t, store, "log", "-r", sourceNode, "-T", "{node}")); got != sourceNode {
		t.Fatalf("merged node=%s", got)
	}
	check("GET", detail, nil, 200)
	t.Log("actual startup, HTTPS push, fork form/status, exact source PR, comparison, formal review and same-operation merge passed without Git on server PATH")
}

func waitCollaborationIndex(t *testing.T, a *application, repoID int64, node string) {
	t.Helper()
	db, err := xorm.NewEngine("sqlite3", models.DbCfg.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for deadline := time.Now().Add(45 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		var state models.ContributionIndexState
		var job models.ContributionJob
		ok, err := db.Id(repoID).Get(&state)
		if err != nil {
			t.Fatal(err)
		}
		found, err := db.Where("kind = ? AND object_id = ?", "repository", repoID).Get(&job)
		if err != nil {
			t.Fatal(err)
		}
		if ok && found && state.Snapshot == node && !state.Updating && !state.Unavailable && job.Requested == job.Completed {
			t.Logf("owned post-push maintenance settled repository=%d generation=%d job=%d completed=%d", repoID, state.Generation, job.ID, job.Completed)
			return
		}
		if time.Now().After(deadline) {
			log, _ := os.ReadFile(filepath.Join(a.root, "server.log"))
			t.Fatalf("post-push index did not settle state=%+v job=%+v server=%s", state, job, log)
		}
	}
}
