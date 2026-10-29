//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Unknwon/macaron"
	"github.com/gogits/gogs/models"
	apiv1 "github.com/gogits/gogs/modules/auth/apiv1"
	"github.com/gogits/gogs/modules/collaboration"
	v1 "github.com/gogits/gogs/routers/api/v1"

	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/routers/repo"
	"github.com/macaron-contrib/binding"
	"github.com/macaron-contrib/csrf"
	"html"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type pullHTTPFixture struct {
	*uiFixture
	service            *collaboration.Service
	source             *models.Repository
	route, local, node string
}

func seededPullHTTP(t *testing.T) *pullHTTPFixture {
	f := pullFixtureHTTP(t)
	s := collaboration.New(uiProvider{f.host})
	source, e := s.Fork(context.Background(), f.owner.Id, collaboration.ForkOptions{SourceID: f.public.Id, OwnerID: f.owner.Id, Name: "proposal-fork"})
	if e != nil {
		t.Fatal(e)
	}
	h := newHgCmd(t, hgPath(t), filepath.Join(f.root, "pull-hg"), 2)
	local := filepath.Join(f.root, "working")
	h.must(t, local, "update", "--clean", "-r", "mark / lab")
	for name, body := range map[string]string{"proposal.txt": "line one\nline two\n", "binary.dat": "\x00changed\x01", "empty.txt": ""} {
		if e := os.WriteFile(filepath.Join(local, name), []byte(body), 0600); e != nil {
			t.Fatal(e)
		}
	}
	h.must(t, local, "add")
	h.must(t, local, "commit", "-u", "Fixture", "-m", "proposal changes")
	node := strings.TrimSpace(h.stdout(t, local, "log", "-r", ".", "-T", "{node}"))
	sourcePath, _ := models.RepositoryStoragePath(f.host.RepositoryRoot(), source.Id)
	h.must(t, sourcePath, "pull", local)
	h.must(t, sourcePath, "bookmark", "-f", "-r", node, "proposal")
	return &pullHTTPFixture{f, s, source, "/fixtureowner/" + f.public.Name + "/pulls", local, node}
}
func (f *pullHTTPFixture) create(t *testing.T) models.PullRequest {
	t.Helper()
	w := f.post(t, f.route+"/new", "owner", url.Values{"title": {"Actual fork proposal"}, "content": {"**Safe description** <script>bad()</script>"}, "source_repo_id": {fmt.Sprint(f.source.Id)}, "source_kind": {"bookmark"}, "source_name": {"proposal"}, "target_kind": {"bookmark"}, "target_name": {"mark / lab"}}, true)
	if w.Code != 303 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	p, e := f.service.GetPull(context.Background(), f.owner.Id, f.public.Id, 1)
	if e != nil {
		t.Fatal(e)
	}
	if p.SourceRepoID != f.source.Id || p.AuthorID != f.owner.Id {
		t.Fatal("server identity lost", p)
	}
	return p
}

func TestPullSourceNameForm(t *testing.T) {
	f := seededPullHTTP(t)
	name := f.owner.Name + "/" + f.source.Name
	w := f.get(t, f.route+"/new", "owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="source_repository"`) || strings.Contains(w.Body.String(), `<select id="pull-source-repo"`) {
		t.Fatal("exact source field missing")
	}
	values := url.Values{"title": {"Exact source"}, "content": {"Retained body"}, "source_repository": {name}, "source_repo_id": {fmt.Sprint(f.public.Id)}, "source_kind": {"bookmark"}, "source_name": {"proposal"}, "target_kind": {"bookmark"}, "target_name": {"mark / lab"}}
	// The rendered exact name wins over a conflicting legacy numeric field.
	w = f.post(t, f.route+"/new", "owner", values, true)
	if w.Code != 303 {
		t.Fatalf("exact source create: %d %s", w.Code, w.Body.String())
	}
	p, err := f.service.GetPull(context.Background(), f.owner.Id, f.public.Id, 1)
	if err != nil || p.SourceRepoID != f.source.Id {
		t.Fatalf("numeric ID overrode name: %+v %v", p, err)
	}
	values.Set("source_repository", "unknown-owner/missing-repo")
	w = f.post(t, f.route+"/new", "owner", values, true)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `value="unknown-owner/missing-repo"`) || !strings.Contains(w.Body.String(), "Retained body") {
		t.Fatalf("denied name not retained: %d %s", w.Code, w.Body.String())
	}
	if _, err := f.db.Id(p.ID).Cols("state").Update(&models.PullRequest{State: models.PullMerged}); err != nil {
		t.Fatal(err)
	}
	w = f.get(t, f.route+"/1", "owner")
	if w.Code != 200 || strings.Contains(w.Body.String(), `action="/code`+f.route+`/1/refresh"`) {
		t.Fatal("merged refresh control still offered")
	}
	w = f.post(t, f.route+"/1/refresh", "owner", url.Values{}, true)
	if w.Code != 409 {
		t.Fatalf("merged refresh accepted: %d", w.Code)
	}
}

func TestPullReviewerErrorRetainsText(t *testing.T) {
	f := pullFixtureHTTP(t)
	p, err := models.CreatePullRecord(context.Background(), f.owner.Id, models.PullRequest{SourceRepoID: f.public.Id, TargetRepoID: f.public.Id, Title: "Reviewer form", SourceSelection: `{"Ref":{"Kind":"node","Name":"1111111111111111111111111111111111111111"}}`, TargetSelection: `{"Ref":{"Kind":"node","Name":"2222222222222222222222222222222222222222"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if err = models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.public.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if err = models.AssignPullReviewers(context.Background(), f.owner.Id, p.ID, []int64{f.reader.Id}); err != nil {
		t.Fatal(err)
	}
	if err := models.ClearRepositoryVisits(context.Background(), f.owner.Id); err != nil {
		t.Fatal(err)
	}
	text := "missing-reviewer"
	w := f.post(t, "/fixtureowner/"+f.public.Name+"/pulls/1/reviewers", "owner", url.Values{"reviewers": {text}}, true)
	if w.Code == 303 || hiddenPull(t, w.Body.String(), "reviewers") != text {
		t.Fatalf("submitted reviewers changed: status=%d", w.Code)
	}
	for _, capability := range []string{`data-popout-target="hg-search"`, `data-popout-target="hg-recents"`} {
		if !strings.Contains(w.Body.String(), capability) {
			t.Errorf("validation response lost %s", capability)
		}
	}
	visits, err := models.RecentRepositories(context.Background(), f.owner.Id)
	if err != nil || len(visits) != 0 {
		t.Fatalf("POST error recorded a visit: %v %v", visits, err)
	}
}

func TestPullInlineCommentRetryRetainsAnchor(t *testing.T) {
	f := seededPullHTTP(t)
	p := f.create(t)
	comparison, err := f.service.RefreshComparison(context.Background(), f.owner.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// A newer pending allocation makes the intended historical identity
	// different from the current comparison, without changing its artifact.
	current, err := models.ReservePullComparison(context.Background(), f.owner.Id, p.ID)
	if err != nil || current.ID == comparison.ID {
		t.Fatalf("replacement comparison: %+v %v", current, err)
	}
	path := f.route + "/1/comments"
	body := "Keep **this inline comment** <literal>"
	w := f.post(t, path, "reader", url.Values{"content": {body}, "comparison_id": {fmt.Sprint(comparison.ID)}, "path": {"proposal.txt"}, "side": {"source"}, "line": {"999"}}, true)
	if w.Code != 422 {
		t.Fatalf("invalid inline line: %d %s", w.Code, w.Body.String())
	}
	form := regexp.MustCompile(`(?s)<form[^>]*action="` + regexp.QuoteMeta(f.prefix+path) + `"[^>]*>(.*?)</form>`).FindStringSubmatch(w.Body.String())
	if len(form) != 2 {
		t.Fatal("comment form missing on error")
	}
	values := url.Values{}
	for name, id := range map[string]string{"comparison_id": "comment-comparison", "path": "comment-path", "line": "comment-line"} {
		field := regexp.MustCompile(`<input\b[^>]*id="` + id + `"[^>]*\bvalue="([^"]*)"`).FindStringSubmatch(form[1])
		if len(field) != 2 {
			t.Fatalf("submitted anchor field %s was discarded", name)
		}
		values.Set(name, html.UnescapeString(field[1]))
	}
	if values.Get("comparison_id") != fmt.Sprint(comparison.ID) || values.Get("path") != "proposal.txt" || values.Get("line") != "999" {
		t.Fatalf("anchor changed on error: %v", values)
	}
	selectField := regexp.MustCompile(`(?s)<select[^>]*id="comment-side"[^>]*>(.*?)</select>`).FindStringSubmatch(form[1])
	if len(selectField) != 2 || !strings.Contains(selectField[1], `<option value="source" selected>`) || !strings.Contains(form[1], "<details open>") {
		t.Fatal("inline section collapsed or source side selection lost")
	}
	textarea := regexp.MustCompile(`(?s)<textarea[^>]*id="pull-comment"[^>]*>(.*?)</textarea>`).FindStringSubmatch(form[1])
	if len(textarea) != 2 || html.UnescapeString(textarea[1]) != body {
		t.Fatal("submitted inline body was changed")
	}
	comments, err := models.ListPullComments(context.Background(), f.reader.Id, p.ID)
	if err != nil || len(comments) != 0 {
		t.Fatalf("failed inline comment saved: %+v %v", comments, err)
	}
	// Retry only the corrected line, preserving the rendered historical anchor.
	values.Set("line", "1")
	values.Set("side", "source")
	values.Set("content", html.UnescapeString(textarea[1]))
	w = f.post(t, path, "reader", values, true)
	comments, err = models.ListPullComments(context.Background(), f.reader.Id, p.ID)
	if w.Code != 303 || err != nil || len(comments) != 1 {
		t.Fatalf("corrected retry: status=%d comments=%+v err=%v", w.Code, comments, err)
	}
	c := comments[0]
	if c.ComparisonID != comparison.ID || c.Path != "proposal.txt" || c.Side != "source" || c.Line != 1 || c.Body != body || !c.Outdated {
		t.Fatalf("retry became general or rebound to current comparison: %+v", c)
	}
	if w.Header().Get("Location") != f.prefix+f.route+fmt.Sprintf("/1#comment-%d", c.ID) {
		t.Fatal("retry did not redirect to canonical anchored comment", w.Header().Get("Location"))
	}
}
func expectPullPost(t *testing.T, f *uiFixture, path, who string, values url.Values, code int) *httptest.ResponseRecorder {
	t.Helper()
	w := f.post(t, path, who, values, true)
	if w.Code != code {
		t.Fatalf("POST %s: %d want %d %s", path, w.Code, code, w.Body.String())
	}
	return w
}
func hiddenPull(t *testing.T, body, name string) string {
	t.Helper()
	m := regexp.MustCompile(`name="` + regexp.QuoteMeta(name) + `" value="([^"]*)"`).FindStringSubmatch(body)
	if len(m) != 2 {
		t.Fatalf("missing hidden %s", name)
	}
	return html.UnescapeString(m[1])
}

func pullFixtureHTTP(t *testing.T) *uiFixture {
	f := newUIFixture(t, "/code")
	if err := collaboration.ReconcileComparisons(context.Background(), f.host); err != nil {
		t.Fatal(err)
	}
	f.m.Map(collaboration.New(uiProvider{f.host}))
	f.m.Post("/api/v1/markdown", binding.BindIgnErr(apiv1.MarkdownForm{}), v1.Markdown)
	guard := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	protected := func(ctx *macaron.Context, token csrf.CSRF) { csrf.Validate(ctx, token) }
	f.m.Get("/pulls", guard, repo.Pulls)
	f.m.Group("/:username/:reponame/pulls", func() {
		f.m.Get("/new", guard, repo.PullNew)
		f.m.Post("/new", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullNewPost)
		f.m.Get("/:number/operations/:operation", repo.PullOperation)
		f.m.Get("/:number/comparisons/:comparison/files/*", repo.PullDownload)
		f.m.Get("/:number/comparisons/:comparison/files/", repo.PullDownload)
		f.m.Post("/:number/edit", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullEdit)
		f.m.Post("/:number/state", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullState)
		f.m.Post("/:number/refresh", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullRefresh)
		f.m.Post("/:number/reviewers", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullReviewers)
		f.m.Post("/:number/review", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullReview)
		f.m.Post("/:number/review/dismiss", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullDismiss)
		f.m.Post("/:number/comments", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullComment)
		f.m.Post("/:number/threads/:thread/resolve", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullResolve)
		f.m.Post("/:number/merge", guard, protected, binding.BindIgnErr(repo.PullForm{}), repo.PullMerge)
		f.m.Get("/:number", repo.PullView)
		f.m.Get("", repo.Pulls)
	}, middleware.RepoAssignment(true), repo.PullRequestLimits)
	return f
}

func TestPullHTTPWorkflow(t *testing.T) {
	f := seededPullHTTP(t)
	root := f.route
	w := f.get(t, root+"/new", "owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `name="source_kind"`) || !strings.Contains(w.Body.String(), `name="target_kind"`) {
		t.Fatalf("typed create form: %d %s", w.Code, w.Body.String())
	}
	p := f.create(t)
	detail := root + "/1"
	if w = f.get(t, detail, "owner"); !strings.Contains(w.Body.String(), `data-pull-state="unprepared"`) {
		t.Fatal("new PR claims preparation is running", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `<dd><code></code></dd>`) || strings.Contains(w.Body.String(), "These full changeset IDs") {
		t.Error("unprepared comparison shows empty pins")
	}
	expectPullPost(t, f.uiFixture, detail+"/refresh", "owner", url.Values{}, 303)
	p, _ = f.service.GetPull(context.Background(), f.owner.Id, f.public.Id, 1)
	if err := models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.public.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	expectPullPost(t, f.uiFixture, detail+"/reviewers", "owner", url.Values{"reviewers": {f.reader.Name}}, 303)
	assigned := f.get(t, "/pulls?filter=assigned", "reader")
	if !strings.Contains(assigned.Body.String(), "Actual fork proposal") {
		t.Fatal("assigned query omitted PR")
	}
	expectPullPost(t, f.uiFixture, detail+"/comments", "reader", url.Values{"content": {"Reader **comment**"}}, 303)
	c := expectPullPost(t, f.uiFixture, detail+"/comments", "reader", url.Values{"content": {"Inline reader"}, "comparison_id": {fmt.Sprint(p.CurrentComparisonID)}, "side": {"source"}, "path": {"proposal.txt"}, "line": {"2"}}, 303)
	if !strings.HasSuffix(c.Header().Get("Location"), "#comment-2") {
		t.Fatal("canonical comment anchor", c.Header())
	}
	expectPullPost(t, f.uiFixture, detail+"/threads/2/resolve", "owner", url.Values{"resolved": {"true"}}, 303)
	expectPullPost(t, f.uiFixture, detail+"/threads/2/resolve", "reader", url.Values{"resolved": {"false"}}, 303)
	reviewer := f.reader
	outsider := &models.User{Name: "otherreader", Email: "other@example.invalid", Passwd: "password", IsActive: true}
	if e := models.CreateUser(outsider); e != nil {
		t.Fatal(e)
	}
	f.reader = outsider
	expectPullPost(t, f.uiFixture, detail+"/threads/2/resolve", "reader", url.Values{"resolved": {"true"}}, 403)
	f.reader = reviewer
	expectPullPost(t, f.uiFixture, detail+"/review", "reader", url.Values{"comparison_id": {fmt.Sprint(p.CurrentComparisonID)}, "state": {"changes_requested"}}, 303)
	expectPullPost(t, f.uiFixture, detail+"/reviewers", "owner", url.Values{"reviewers": {""}}, 409)
	expectPullPost(t, f.uiFixture, detail+"/review", "reader", url.Values{"comparison_id": {fmt.Sprint(p.CurrentComparisonID)}, "state": {"approved"}}, 303)
	expectPullPost(t, f.uiFixture, detail+"/edit", "owner", url.Values{"title": {"Edited actual proposal"}, "content": {"**saved**"}}, 303)
	expectPullPost(t, f.uiFixture, detail+"/state", "owner", url.Values{"state": {"declined"}}, 303)
	if body := f.get(t, root+"?state=declined", "owner").Body.String(); !strings.Contains(body, "Edited actual proposal") {
		t.Fatal("declined list")
	}
	expectPullPost(t, f.uiFixture, detail+"/state", "owner", url.Values{"state": {"open"}}, 303)
	body := f.get(t, detail, "owner").Body.String()
	if strings.Contains(body, "<script>bad()") || !strings.Contains(body, "proposal-fork") {
		t.Fatal("unsafe body or source identity")
	}
	values := url.Values{"confirm": {"true"}, "comparison_id": {fmt.Sprint(p.CurrentComparisonID)}, "source_node": {hiddenPull(t, body, "source_node")}, "target_node": {hiddenPull(t, body, "target_node")}, "merge_key": {hiddenPull(t, body, "merge_key")}}
	w = expectPullPost(t, f.uiFixture, detail+"/merge", "owner", values, 303)
	operation := strings.TrimPrefix(w.Header().Get("Location"), f.prefix)
	var state string
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		request := httptest.NewRequest("GET", f.prefix+operation, nil)
		request.Header.Set("X-Fixture-User", "owner")
		request.Header.Set("Accept", "application/json")
		response := httptest.NewRecorder()
		f.m.ServeHTTP(response, request)
		var result struct{ State, Code string }
		if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil {
			t.Fatalf("operation JSON: %s", response.Body.String())
		}
		state = result.State
		if state == "merged" {
			break
		}
		if state == "failed" {
			t.Fatalf("merge failed: %+v", result)
		}
	}
	if state != "merged" {
		t.Fatalf("async operation did not merge: %s", state)
	}
	expectPullPost(t, f.uiFixture, detail+"/merge", "owner", values, 303)
	if body := f.get(t, root+"?state=merged", "owner").Body.String(); !strings.Contains(body, "Edited actual proposal") {
		t.Fatal("merged list")
	}
}

func TestPullHTTPDenialAndCSRF(t *testing.T) {
	f := pullFixtureHTTP(t)
	root := "/fixtureowner/" + f.public.Name + "/pulls"
	w := f.post(t, root+"/new", "owner", url.Values{"title": {"proposal"}}, false)
	if w.Code != 400 {
		t.Fatalf("missing CSRF: %d", w.Code)
	}
	w = f.get(t, "/pulls", "owner")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Pull requests") {
		t.Fatalf("workspace list: %d", w.Code)
	}
	for _, field := range []string{"actor_id", "target_repo_id", "author_id"} {
		expectPullPost(t, f, root+"/new", "owner", url.Values{field: {"999"}}, 422)
	}
	if w := f.get(t, "/fixtureowner/private-lab/pulls", ""); w.Code != 404 {
		t.Fatal("private list disclosed", w.Code)
	}
}

func TestPullUIStaleConflictAndBinary(t *testing.T) {
	f := seededPullHTTP(t)
	root := f.route
	w := f.get(t, root, "reader")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "No pull requests") {
		t.Fatalf("real empty list: %d %s", w.Code, w.Body.String())
	}
	p := f.create(t)
	detail := root + "/1"
	expectPullPost(t, f.uiFixture, detail+"/refresh", "owner", url.Values{}, 303)
	p, _ = f.service.GetPull(context.Background(), f.owner.Id, f.public.Id, 1)
	comparison, e := models.GetPullComparison(context.Background(), f.owner.Id, p.ID, p.CurrentComparisonID)
	if e != nil {
		t.Fatal(e)
	}
	body := f.get(t, detail, "owner").Body.String()
	if !strings.Contains(body, "Binary changes") {
		t.Fatal("binary notice missing", body)
	}
	for file, payload := range map[string]string{"binary.dat": "\x00changed\x01", "empty.txt": "", "proposal.txt": "line one\nline two\n"} {
		response := f.get(t, fmt.Sprintf("%s/comparisons/%d/files/%s?side=source", detail, comparison.ID, file), "reader")
		if response.Code != 200 || response.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(response.Header().Get("Content-Disposition"), "attachment") {
			t.Fatal("artifact download", file, response.Code, response.Header())
		}
		if response.Body.String() != payload {
			t.Errorf("%s payload = %q, want %q", file, response.Body.String(), payload)
		}
	}
	for _, file := range []string{"absent", "..%2f.hg%2fhgrc"} {
		response := f.get(t, fmt.Sprintf("%s/comparisons/%d/files/%s?side=source", detail, comparison.ID, file), "owner")
		if response.Code != 404 || response.Header().Get("Content-Disposition") != "" {
			t.Fatal("unsafe/missing artifact", response.Code, response.Header())
		}
	}
	expectPullPost(t, f.uiFixture, detail+"/comments", "reader", url.Values{"content": {"Old inline"}, "comparison_id": {fmt.Sprint(comparison.ID)}, "side": {"source"}, "path": {"proposal.txt"}, "line": {"1"}}, 303)
	h := newHgCmd(t, hgPath(t), filepath.Join(f.root, "advance-hg"), 2)
	if e := os.WriteFile(filepath.Join(f.local, "proposal.txt"), []byte("updated line\n"), 0600); e != nil {
		t.Fatal(e)
	}
	h.must(t, f.local, "commit", "-u", "Fixture", "-m", "advance source")
	next := strings.TrimSpace(h.stdout(t, f.local, "log", "-r", ".", "-T", "{node}"))
	sourcePath, _ := models.RepositoryStoragePath(f.host.RepositoryRoot(), f.source.Id)
	h.must(t, sourcePath, "pull", f.local)
	h.must(t, sourcePath, "bookmark", "-f", "-r", next, "proposal")
	body = f.get(t, detail, "owner").Body.String()
	if !strings.Contains(body, `data-pull-state="stale"`) || strings.Contains(body, "data-pull-merge") {
		t.Fatal("stale form still mergeable")
	}
	expectPullPost(t, f.uiFixture, detail+"/refresh", "owner", url.Values{}, 303)
	body = f.get(t, detail, "owner").Body.String()
	if !strings.Contains(body, "Outdated") {
		t.Fatal("historical thread lost")
	}
	expectPullPost(t, f.uiFixture, detail+"/merge", "owner", url.Values{"confirm": {"true"}, "comparison_id": {fmt.Sprint(comparison.ID)}, "source_node": {string(comparison.SourceNode)}, "target_node": {string(comparison.TargetNode)}, "merge_key": {"stale-operation-key"}}, 409)
	// The historical artifact remains immutable after refreshing.
	response := f.get(t, fmt.Sprintf("%s/comparisons/%d/files/proposal.txt?side=source", detail, comparison.ID), "reader")
	if response.Body.String() != "line one\nline two\n" {
		t.Fatal("historical artifact relabeled", response.Body.String())
	}
	// Repair a deleted source reference through the real CAS-bound form.
	saved, _ := f.service.GetPull(context.Background(), f.owner.Id, f.public.Id, 1)
	h.must(t, sourcePath, "bookmark", "--delete", "proposal")
	if body = f.get(t, detail, "owner").Body.String(); !strings.Contains(body, `data-pull-state="missing-ref"`) {
		t.Fatal("missing ref not distinguished")
	}
	selectors := url.Values{"expected_source": {saved.SourceSelection}, "expected_target": {saved.TargetSelection}, "comparison_id": {fmt.Sprint(saved.CurrentComparisonID)}, "source_kind": {"node"}, "source_name": {next}, "target_kind": {"bookmark"}, "target_name": {"mark / lab"}}
	expectPullPost(t, f.uiFixture, detail+"/refresh", "owner", selectors, 303)
	expectPullPost(t, f.uiFixture, detail+"/refresh", "owner", selectors, 409)
	updated, _ := f.service.GetPull(context.Background(), f.owner.Id, f.public.Id, 1)
	if updated.SourceRepoID != f.source.Id || updated.TargetRepoID != f.public.Id {
		t.Fatal("reselection changed repository identity")
	}

}

func TestProfileContributionPublicPullDestination(t *testing.T) {
	f := seededPullHTTP(t)
	p := f.create(t)
	c, err := f.service.RefreshComparison(context.Background(), f.owner.Id, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.public.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	if err = models.AssignPullReviewers(context.Background(), f.owner.Id, p.ID, []int64{f.reader.Id}); err != nil {
		t.Fatal(err)
	}
	if err = models.SubmitPullReview(context.Background(), f.reader.Id, p.ID, c.ID, models.ReviewApproved); err != nil {
		t.Fatal(err)
	}
	w := f.get(t, f.route+"/1", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Actual fork proposal") || !strings.Contains(w.Body.String(), f.reader.Name) {
		t.Fatalf("public contribution destination HTTP %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), `action="/code`+f.route+`/1/review"`) {
		t.Fatal("guest transcript exposed review mutation form")
	}
}
