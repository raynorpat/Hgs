package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
)

const viewNode = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const viewNode2 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type viewApp struct {
	handler      http.Handler
	holder       *hostingHolder
	root, config string
	alice, bob   *models.User
	repo         *models.Repository
}

func newViewApp(t *testing.T) *viewApp { return newViewAppHg(t, "") }
func newViewAppHg(t *testing.T, executable string) *viewApp {
	return newViewAppPrefix(t, executable, "/gogs")
}
func newViewAppPrefix(t *testing.T, executable, subURL string) *viewApp {
	t.Helper()
	root := t.TempDir()
	source, _ := filepath.Abs("..")
	oldDB, oldHas := models.DbCfg, models.HasEngine
	oldStatic, oldConf, oldCustom := setting.StaticRootPath, setting.ConfRootPath, setting.CustomPath
	oldCache, oldSession, oldLangs, oldNames := setting.CacheAdapter, setting.SessionConfig, setting.Langs, setting.Names
	oldService, oldMerc, oldRepo, oldKey := setting.Service, setting.Mercurial, setting.RepoRootPath, setting.SSHHostKeyPath
	oldURL, oldSub, oldSSH, oldInstall := setting.AppUrl, setting.AppSubUrl, setting.DisableSSH, setting.InstallLock
	oldLog := setting.LogRootPath
	t.Cleanup(func() {
		models.CloseEngine()
		models.DbCfg, models.HasEngine = oldDB, oldHas
		setting.StaticRootPath, setting.ConfRootPath, setting.CustomPath = oldStatic, oldConf, oldCustom
		setting.CacheAdapter, setting.SessionConfig, setting.Langs, setting.Names = oldCache, oldSession, oldLangs, oldNames
		setting.Service, setting.Mercurial, setting.RepoRootPath, setting.SSHHostKeyPath = oldService, oldMerc, oldRepo, oldKey
		setting.AppUrl, setting.AppSubUrl, setting.DisableSSH, setting.InstallLock = oldURL, oldSub, oldSSH, oldInstall
		setting.LogRootPath = oldLog
	})
	models.DbCfg.Type = "sqlite3"
	models.DbCfg.Path = filepath.Join(root, "db")
	setting.LogRootPath = root
	if err := models.NewEngine(); err != nil {
		t.Fatal(err)
	}
	models.HasEngine = true
	setting.StaticRootPath = source
	setting.ConfRootPath = filepath.Join(source, "conf")
	setting.CustomPath = root
	setting.CacheAdapter = "memory"
	setting.SessionConfig.Provider = "memory"
	setting.SessionConfig.CookieName = "view_session"
	setting.Langs = []string{"en-US"}
	setting.Names = []string{"English"}
	setting.Service.RequireSignInView = false
	setting.Service.EnableNotifyMail = false
	setting.Service.EnableReverseProxyAuth = false
	if executable == "" {
		executable, _ = os.Executable()
	}
	setting.Mercurial.Executable = executable
	setting.Mercurial.ConfigPath = filepath.Join(root, "config", "hgrc")
	setting.Mercurial.MaxProcesses = 2
	setting.Mercurial.SessionTimeout = time.Minute
	setting.RepoRootPath = filepath.Join(root, "repos")
	setting.SSHHostKeyPath = filepath.Join(root, "hostkey")
	setting.AppUrl = "http://example.test" + subURL + "/"
	setting.AppSubUrl = subURL
	setting.DisableSSH = true
	setting.InstallLock = true
	a := &viewApp{root: root, config: filepath.Dir(setting.Mercurial.ConfigPath), alice: &models.User{Name: "alice", Email: "alice@example.test", Passwd: "password", IsActive: true}, bob: &models.User{Name: "bob", Email: "bob@example.test", Passwd: "password", IsActive: true}}
	for _, u := range []*models.User{a.alice, a.bob} {
		if err := models.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	a.holder = &hostingHolder{errors: make(chan error, 1)}
	if err := a.holder.Initialize(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := a.holder.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	s := a.holder.Service()
	var err error
	a.repo, err = models.CreateHgRepository(context.Background(), s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: a.alice.Id, Name: "project"})
	if err != nil {
		t.Fatal(err)
	}
	a.handler = newWebHandler(a.holder)
	return a
}
func (a *viewApp) request(t *testing.T, method, path, body, user string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://host"+path, strings.NewReader(body))
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if user != "" {
		r.SetBasicAuth(user, "password")
	}
	w := httptest.NewRecorder()
	a.handler.ServeHTTP(w, r)
	return w
}
func (a *viewApp) fixture(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(a.config, name), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}
func (a *viewApp) calls() int {
	b, _ := os.ReadFile(filepath.Join(a.config, "calls"))
	return len(strings.Fields(string(b)))
}
func (a *viewApp) populate(t *testing.T, n int) {
	a.fixture(t, "refs", fmt.Sprintf(`{"rev":1,"node":%q,"branch":"default","head":"1","bookmarks":["default","slash/ref"],"tags":["default"]}`+"\n", viewNode))
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "644   f%03d.txt\n", i)
	}
	a.fixture(t, "manifest", b.String())
}
func TestEmptyRepositoryStillHasIssues(t *testing.T) {
	a := newViewApp(t)
	before := a.calls()
	w := a.request(t, "GET", "/gogs/alice/project/issues", "", "")
	if w.Code != 200 || a.calls() != before {
		t.Fatalf("empty issues: %d history=%d %s", w.Code, a.calls()-before, w.Body.String())
	}
	w = a.request(t, "GET", "/gogs/alice/project", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hg clone") || !strings.Contains(w.Body.String(), "/gogs/alice/project/issues") {
		t.Fatalf("empty home: %d %s", w.Code, w.Body.String())
	}
}
func TestTreeViewBoundedInvocations(t *testing.T) {
	a := newViewApp(t)
	for _, n := range []int{1, 200} {
		a.populate(t, n)
		before := a.calls()
		start := time.Now()
		w := a.request(t, "GET", "/gogs/alice/project/src/"+viewNode, "", "")
		t.Logf("tree %d latency %s calls %d", n, time.Since(start), a.calls()-before)
		if w.Code != 200 || a.calls()-before > 5 || !strings.Contains(w.Body.String(), fmt.Sprintf("f%03d.txt", n-1)) {
			t.Fatalf("tree %d status %d calls %d body %s", n, w.Code, a.calls()-before, w.Body.String())
		}
	}
}
func TestMercurialViewSelection(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	a.fixture(t, "refs", fmt.Sprintf(`{"rev":1,"node":%q,"branch":"default","head":"1"}`+"\n"+`{"rev":2,"node":%q,"branch":"default","head":"1","closed":"1","bookmarks":["default","slash/ref"]}`+"\n"+`{"rev":3,"node":%q,"branch":"another","head":"1","tags":["default"]}`+"\n", viewNode, viewNode2, strings.Repeat("c", 40)))
	for kind, node := range map[string]string{"branch": viewNode, "bookmark": viewNode2, "tag": strings.Repeat("c", 40)} {
		q := url.Values{"ref_kind": {kind}, "ref_name": {"default"}}
		w := a.request(t, "GET", "/gogs/alice/project?"+q.Encode(), "", "")
		if w.Code != 302 || w.Header().Get("Location") != "/gogs/alice/project/src/"+node {
			t.Fatalf("%s %d %s", kind, w.Code, w.Header().Get("Location"))
		}
	}

	w := a.request(t, "GET", "/gogs/alice/project?ref_kind=bookmark&ref_name=slash%2Fref", "", "")
	if w.Code != 302 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = a.request(t, "GET", "/gogs/alice/project/branches", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "closed") || !strings.Contains(w.Body.String(), viewNode2) {
		t.Fatal("heads/closed refs", w.Code, w.Body.String())
	}
	w = a.request(t, "GET", "/gogs/alice/project/src/"+viewNode+"/literal%252f?ref_kind=bookmark&ref_name=slash%2Fref", "", "")
	if w.Code != 302 || w.Header().Get("Location") != "/gogs/alice/project/src/"+viewNode2+"/literal%252f" {
		t.Fatal("selection path identity", w.Code, w.Header().Get("Location"))
	}

	for _, p := range []string{"/src/bad", "/commits?cursor=bad"} {
		before := a.calls()
		w = a.request(t, "GET", "/gogs/alice/project"+p, "", "")
		if w.Code != 400 || a.calls() != before {
			t.Fatalf("malformed %s: %d calls %d", p, w.Code, a.calls()-before)
		}
	}
}

type viewBrowser struct {
	a       *viewApp
	cookies map[string]*http.Cookie
	token   string
}

func (a *viewApp) browser(t *testing.T, user string) *viewBrowser {
	b := &viewBrowser{a: a, cookies: map[string]*http.Cookie{}}
	b.do(t, "GET", "/gogs/user/login", nil)
	if user != "" {
		w := b.do(t, "POST", "/gogs/user/login", url.Values{"uname": {user}, "password": {"password"}})
		if w.Code != 302 {
			t.Fatalf("login: %d %s", w.Code, w.Body.String())
		}
	}
	return b
}
func (b *viewBrowser) do(t *testing.T, method, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	data := ""
	if form != nil {
		data = form.Encode()
	}
	r := httptest.NewRequest(method, "http://host"+path, strings.NewReader(data))
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	if method == "POST" {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("X-Csrf-Token", b.token)
	}
	w := httptest.NewRecorder()
	b.a.handler.ServeHTTP(w, r)
	for _, c := range w.Result().Cookies() {
		b.cookies[c.Name] = c
	}
	re := regexp.MustCompile(`<meta name="_csrf" content="([^"]+)"`)
	if m := re.FindStringSubmatch(w.Body.String()); m != nil {
		b.token = m[1]
	}
	return w
}
func TestFileRenderingCannotExecuteMarkup(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	a.fixture(t, "manifest", "644   source.txt\n644   README.md\n644   binary\n644   big\n")
	for _, name := range []string{"source.txt", "README.md"} {
		a.fixture(t, "content", "<script>alert(1)</script>\n<img src=x onerror=alert(2)>\n")
		w := a.request(t, "GET", "/gogs/alice/project/src/"+viewNode+"/"+name, "", "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "<script>alert(1)") || strings.Contains(w.Body.String(), "<img src=x") {
			t.Fatalf("unsafe %s: %d %s", name, w.Code, w.Body.String())
		}
	}
	a.fixture(t, "content", "\x00binary")
	w := a.request(t, "GET", "/gogs/alice/project/src/"+viewNode+"/binary", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Binary content") {
		t.Fatal("binary notice", w.Code, w.Body.String())
	}
	a.fixture(t, "content", strings.Repeat("x", (1<<20)+2))
	w = a.request(t, "GET", "/gogs/alice/project/src/"+viewNode+"/big", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "too large to display") {
		t.Fatal("oversize notice", w.Code)
	}
	a.fixture(t, "patch", "+<script>alert(99)</script>\n")
	w = a.request(t, "GET", "/gogs/alice/project/commit/"+viewNode, "", "")
	if w.Code != 200 || strings.Contains(w.Body.String(), "<script>alert(99)") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatal("patch escaped", w.Code, w.Body.String())
	}
}
func TestRawPathIdentity(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	raw := string([]byte{'d', '/', 0xff})
	a.fixture(t, "manifest", "644   A #?%.txt\n644   %2f\n644   "+raw+"\n")
	for _, p := range []string{"A%20%23%3F%25.txt", "%252f", "?path=" + base64.RawURLEncoding.EncodeToString([]byte(raw))} {
		a.fixture(t, "content", "raw\x00bytes")
		prefix := "/"
		if strings.HasPrefix(p, "?") {
			prefix = ""
		}
		w := a.request(t, "GET", "/gogs/alice/project/raw/"+viewNode+prefix+p, "", "")
		if w.Code != 200 || w.Body.String() != "raw\x00bytes" || w.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatalf("raw %s %d %v %q", p, w.Code, w.Header(), w.Body.String())
		}
	}
	for _, p := range []string{"a%20%23%3F%25.txt", "%2f", "%5c", "x?path=eA"} {
		w := a.request(t, "GET", "/gogs/alice/project/raw/"+viewNode+"/"+p, "", "")
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("invalid raw %s: %d", p, w.Code)
		}
	}
}
func TestIssueWorkflowSurvivesMercurialCutover(t *testing.T) {
	a := newViewApp(t)
	b := a.browser(t, "alice")
	before := a.calls()
	w := b.do(t, "GET", "/gogs/alice/project/issues/new", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = b.do(t, "POST", "/gogs/alice/project/issues/new", url.Values{"title": {"keep issues"}, "content": {"first"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok": true`) {
		t.Fatal("create issue", w.Code, w.Body.String())
	}
	issue, err := models.GetIssueByIndex(a.repo.Id, 1)
	if err != nil || issue.Name != "keep issues" {
		t.Fatal(issue, err)
	}
	label := &models.Label{RepoId: a.repo.Id, Name: "bug", Color: "#123456"}
	if err := models.NewLabel(label); err != nil {
		t.Fatal(err)
	}
	milestone := &models.Milestone{RepoId: a.repo.Id, Index: 1, Name: "First"}
	if err := models.NewMilestone(milestone); err != nil {
		t.Fatal(err)
	}
	if err := models.AddAccess(&models.Access{UserId: a.bob.Id, RepoId: a.repo.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path string
		form url.Values
	}{
		{"/issues/1/label", url.Values{"action": {"attach"}, "id": {fmt.Sprint(label.Id)}}},
		{"/issues/1/milestone", url.Values{"issue": {fmt.Sprint(issue.Id)}, "milestoneid": {fmt.Sprint(milestone.Id)}}},
		{"/issues/1/assignee", url.Values{"issue": {fmt.Sprint(issue.Id)}, "assigneeid": {fmt.Sprint(a.bob.Id)}}},
	} {
		w = b.do(t, "POST", "/gogs/alice/project"+tc.path, tc.form)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok": true`) {
			t.Fatal(tc.path, w.Code, w.Body.String())
		}
	}
	issue, err = models.GetIssueByIndex(a.repo.Id, 1)
	if err != nil || issue.AssigneeId != a.bob.Id || issue.MilestoneId != milestone.Id || !strings.Contains(issue.LabelIds, fmt.Sprintf("$%d|", label.Id)) {
		t.Fatalf("issue metadata: %+v %v", issue, err)
	}

	for _, form := range []url.Values{{"issueIndex": {"1"}, "content": {"comment"}}, {"issueIndex": {"1"}, "change_status": {"Close"}}, {"issueIndex": {"1"}, "change_status": {"Reopen"}}} {
		w = b.do(t, "POST", "/gogs/alice/project/comment/new", form)
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok": true`) {
			t.Fatal("comment/status", w.Code, w.Body.String())
		}
	}
	issue, err = models.GetIssueByIndex(a.repo.Id, 1)
	if err != nil || issue.IsClosed {
		t.Fatal(issue, err)
	}
	w = b.do(t, "POST", "/gogs/alice/project/settings", url.Values{"action": {"update"}, "repo_name": {"renamed"}, "desc": {"new description"}})
	if w.Code != 302 {
		t.Fatal("rename", w.Code, w.Body.String())
	}
	w = b.do(t, "GET", "/gogs/alice/renamed/issues/1", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "keep issues") {
		t.Fatal("renamed issue", w.Code, w.Body.String())
	}
	if a.calls() != before {
		t.Fatalf("issue/settings invoked Hg %d times", a.calls()-before)
	}
	w = b.do(t, "POST", "/gogs/alice/renamed/settings", url.Values{"action": {"transfer"}, "repo_name": {"renamed"}, "new_owner_name": {"bob"}, "password": {"password"}})
	if w.Code != 302 {
		t.Fatal("transfer", w.Code, w.Body.String())
	}
	target := a.browser(t, "bob")
	w = target.do(t, "GET", "/gogs/bob/renamed/issues/1", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "keep issues") {
		t.Fatal("transferred issue", w.Code, w.Body.String())
	}
	for _, path := range []string{"/gogs/", "/gogs/bob?tab=activity"} {
		w = target.do(t, "GET", path, nil)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `transferred repository <code>alice/renamed</code> to <a href="/gogs/bob/renamed"`) || strings.Contains(body, "/gogs//gogs/") {
			t.Fatal("retained transfer activity link", path, w.Code, body)
		}
	}
	for _, p := range []string{"/gogs/bob", "/gogs/user/settings", "/gogs/user/settings/ssh"} {
		w = target.do(t, "GET", p, nil)
		if w.Code != 200 {
			t.Fatal("account smoke", p, w.Code)
		}
	}
	if a.calls() != before {
		t.Fatalf("transfer/account invoked Hg %d times", a.calls()-before)
	}

	w = b.do(t, "POST", "/gogs/repo/create", url.Values{"uid": {fmt.Sprint(a.bob.Id)}, "repo_name": {"forged"}})
	if w.Code != 403 {
		t.Fatal("forged personal owner", w.Code, w.Body.String())
	}
}
func TestPrivateIssueAttachments(t *testing.T) {
	a := newViewApp(t)
	a.repo.IsPrivate = true
	if err := models.UpdateRepository(a.repo); err != nil {
		t.Fatal(err)
	}
	one := &models.Issue{RepoId: a.repo.Id, Index: 1, Name: "private", PosterId: a.alice.Id}
	two := &models.Issue{RepoId: a.repo.Id, Index: 2, Name: "other", PosterId: a.alice.Id}
	for _, i := range []*models.Issue{one, two} {
		if err := models.NewIssue(i); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(a.root, "secret")
	os.WriteFile(file, []byte("secret attachment"), 0600)
	att, err := models.CreateAttachment(one.Id, 0, "file.txt", file)
	if err != nil {
		t.Fatal(err)
	}
	s := a.holder.Service()
	pub, err := models.CreateHgRepository(context.Background(), s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: a.alice.Id, Name: "public"})
	if err != nil {
		t.Fatal(err)
	}
	pubIssue := &models.Issue{RepoId: pub.Id, Index: 1, Name: "public", PosterId: a.alice.Id}
	if err := models.NewIssue(pubIssue); err != nil {
		t.Fatal(err)
	}
	owner := a.browser(t, "alice")
	outsider := a.browser(t, "bob")
	suffix := fmt.Sprintf("/attachment/%d", att.Id)
	for _, tc := range []struct {
		b    *viewBrowser
		path string
		want int
	}{{owner, "/gogs/alice/project/issues/1" + suffix, 200}, {owner, "/gogs/alice/project/issues/2" + suffix, 404}, {outsider, "/gogs/alice/project/issues/1" + suffix, 404}, {outsider, "/gogs/alice/public/issues/1" + suffix, 404}} {
		w := tc.b.do(t, "GET", tc.path, nil)
		if w.Code != tc.want {
			t.Errorf("%s got %d want %d", tc.path, w.Code, tc.want)
		}
		if tc.want == 404 && strings.Contains(w.Body.String(), "secret attachment") {
			t.Error("private attachment disclosed")
		}
	}
}
func TestIssueWorkflowRejectsForgedPersonalOwner(t *testing.T) {
	a := newViewApp(t)
	b := a.browser(t, "alice")
	b.do(t, "GET", "/gogs/repo/create", nil)
	before := a.calls()
	w := b.do(t, "POST", "/gogs/repo/create", url.Values{"uid": {fmt.Sprint(a.bob.Id)}, "repo_name": {"forged"}})
	if w.Code != 403 || a.calls() != before {
		t.Fatalf("forged personal owner: status %d Hg calls %d", w.Code, a.calls()-before)
	}
	if _, err := models.GetRepositoryByName(a.bob.Id, "forged"); err != models.ErrRepoNotExist {
		t.Fatalf("forged repo allocated: %v", err)
	}
}
func TestIssueWorkflowNotificationDelivery(t *testing.T) {
	a := newViewApp(t)
	_, wait := fakeViewSMTP(t)
	mentioned := &models.User{Name: "charlie", Email: "charlie@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(mentioned); err != nil {
		t.Fatal(err)
	}
	commentMention := &models.User{Name: "dana", Email: "dana@example.test", Passwd: "password", IsActive: true}
	if err := models.CreateUser(commentMention); err != nil {
		t.Fatal(err)
	}

	if err := models.WatchRepo(a.bob.Id, a.repo.Id, true); err != nil {
		t.Fatal(err)
	}
	b := a.browser(t, "alice")
	b.do(t, "GET", "/gogs/alice/project/issues/new", nil)
	before := a.calls()
	setting.Service.EnableNotifyMail = true
	w := b.do(t, "POST", "/gogs/alice/project/issues/new", url.Values{"title": {"notification"}, "content": {"hello @charlie"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok": true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	first := wait(2)
	all := first[0].recipients + first[1].recipients
	if !strings.Contains(all, "bob@example.test") || !strings.Contains(all, "charlie@example.test") {
		t.Fatal("watcher/mention delivery", first)
	}
	w = b.do(t, "POST", "/gogs/alice/project/comment/new", url.Values{"issueIndex": {"1"}, "content": {"comment @dana"}})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"ok": true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	comments := wait(2)
	commentRecipients := comments[0].recipients + comments[1].recipients
	if !strings.Contains(commentRecipients, "bob@example.test") || !strings.Contains(commentRecipients, "dana@example.test") || strings.Contains(commentRecipients, "charlie@example.test") {
		t.Fatal("comment did not use its own mentions", comments)
	}

	for _, m := range comments {
		if !strings.Contains(m.body, "notification") {
			t.Fatal("wrong notification", m)
		}
	}
	setting.Service.EnableNotifyMail = false
	if a.calls() != before {
		t.Fatal("notifications invoked Hg")
	}
}
func TestMercurialViewHistoryCursor(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	changeset := func(rev int) string {
		return fmt.Sprintf(`{"node":%q,"rev":%d,"p1node":%q,"p2node":%q,"author":"t","desc":"revision %d","branch":"default","date":[1,0]}`+"\n", viewNode, rev, strings.Repeat("0", 40), strings.Repeat("0", 40), rev)
	}
	a.fixture(t, "changeset", changeset(31))
	var history strings.Builder
	for rev := 31; rev >= 0; rev-- {
		history.WriteString(changeset(rev))
	}
	a.fixture(t, "history", history.String())
	w := a.request(t, "GET", "/gogs/alice/project/commits", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "revision 31") {
		t.Fatal("first history", w.Code, w.Body.String())
	}
	re := regexp.MustCompile(`href="([^"]+/commits\?cursor=[^"]+)"`)
	m := re.FindStringSubmatch(w.Body.String())
	if m == nil {
		t.Fatal("next history missing")
	}
	next := html.UnescapeString(m[1])
	u, _ := url.Parse(next)
	data, err := base64.RawURLEncoding.DecodeString(u.Query().Get("cursor"))
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		Version  int    `json:"v"`
		Node     string `json:"node"`
		Revision int64  `json:"rev"`
		Before   int64  `json:"before"`
		Path     string `json:"path"`
	}
	if err = json.Unmarshal(data, &c); err != nil {
		t.Fatal(err)
	}
	if c.Version != 1 || c.Node != viewNode || c.Revision != 31 || c.Before != 2 || c.Path != "" {
		t.Fatalf("changed history snapshot/bound: %s", data)
	}
	a.fixture(t, "history", changeset(1)+changeset(0))
	w = a.request(t, "GET", next, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "revision 1") || strings.Contains(w.Body.String(), "revision 31") {
		t.Fatal("later history", w.Code, w.Body.String())
	}
	args, _ := os.ReadFile(filepath.Join(a.config, "arguments"))
	if !strings.Contains(string(args), `and (0:1)`) {
		t.Fatalf("history lost exclusive before bound: %s", args)
	}
	// A cursor with a revision that does not match its node must fail, never reset.
	c.Revision = 30
	c.Before = -1
	bad, _ := json.Marshal(c)
	token := base64.RawURLEncoding.EncodeToString(bad)
	w = a.request(t, "GET", "/gogs/alice/project/commits?cursor="+token, "", "")
	if w.Code != 400 {
		t.Fatal("stale cursor reset", w.Code, w.Body.String())
	}
}
func TestRawFileHealthySequentialKeepalive(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	a.fixture(t, "content", "raw healthy\n")
	server := httptest.NewServer(a.handler)
	defer server.Close()
	client := server.Client()
	for i := 0; i < 3; i++ {
		reused := false
		r, _ := http.NewRequest("GET", server.URL+"/gogs/alice/project/raw/"+viewNode+"/f000.txt", nil)
		r = r.WithContext(httptrace.WithClientTrace(r.Context(), &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) { reused = info.Reused }}))
		response, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != "raw healthy\n" {
			t.Fatalf("raw response %d: %d %q %v", i, response.StatusCode, body, err)
		}
		if i > 0 && !reused {
			t.Fatal("healthy raw cleanup poisoned keepalive")
		}
	}
}
func TestEmptyRepositoryCreationUI(t *testing.T) {
	a := newViewApp(t)
	b := a.browser(t, "alice")
	w := b.do(t, "GET", "/gogs/repo/create", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	for _, control := range []string{`name="auto_init"`, `name="gitignore"`, `name="license"`} {
		if strings.Contains(w.Body.String(), control) {
			t.Fatalf("Git initialization control remains: %s", control)
		}
	}
	w = b.do(t, "POST", "/gogs/repo/create", url.Values{"uid": {fmt.Sprint(a.alice.Id)}, "repo_name": {"new-empty"}, "desc": {"created through UI"}})
	if w.Code != 302 || w.Header().Get("Location") != "/gogs/alice/new-empty" {
		t.Fatal("create", w.Code, w.Body.String())
	}
	r, err := models.GetRepositoryByName(a.alice.Id, "new-empty")
	if err != nil || r.State != models.RepositoryReady {
		t.Fatal(r, err)
	}
	w = b.do(t, "GET", "/gogs/alice/new-empty", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "hg clone") {
		t.Fatal("new empty page", w.Code, w.Body.String())
	}
}
func TestMercurialViewReadAuthorization(t *testing.T) {
	a := newViewApp(t)
	a.populate(t, 1)
	a.repo.IsPrivate = true
	if err := models.UpdateRepository(a.repo); err != nil {
		t.Fatal(err)
	}
	before := a.calls()
	for _, p := range []string{"/issues", "/commits", "/src/" + viewNode, "/raw/" + viewNode + "/f000.txt", "/commit/" + viewNode} {
		w := a.request(t, "GET", "/gogs/alice/project"+p, "", "")
		if w.Code != 404 {
			t.Fatal(p, w.Code)
		}
	}
	if a.calls() != before {
		t.Fatal("denied content request invoked Hg")
	}
	if err := models.AddAccess(&models.Access{UserId: a.bob.Id, RepoId: a.repo.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	w := a.request(t, "GET", "/gogs/alice/project/issues", "", "bob")
	if w.Code != 200 {
		t.Fatal("reader issues", w.Code)
	}
	w = a.request(t, "GET", "/gogs/alice/project/settings", "", "bob")
	if w.Code != 404 {
		t.Fatal("reader settings", w.Code)
	}
	a.repo.IsPrivate = false
	if err := models.UpdateRepository(a.repo); err != nil {
		t.Fatal(err)
	}
	setting.Service.RequireSignInView = true
	a.handler = newWebHandler(a.holder)
	w = a.request(t, "GET", "/gogs/alice/project/issues", "", "")
	if w.Code != 302 || w.Header().Get("Location") != "/gogs/user/login" {
		t.Fatal("sign-in-required view", w.Code, w.Header())
	}
}

func TestMercurialViewEndpointPathIdentity(t *testing.T) {
	for _, tc := range []struct{ name, prefix, owner, repo, file string }{
		{"raw directory", "/gogs", "alice", "project", "raw/file.txt"},
		{"commit directory", "/gogs", "alice", "project", "commit/file.txt"},
		{"commit owner", "/gogs", "commit", "project", "file.txt"},
		{"commit repository", "/gogs", "alice", "commit", "file.txt"},
		{"raw prefix", "/gogs/raw", "alice", "project", "file.txt"},
		{"commit prefix", "/gogs/commit", "alice", "project", "file.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newViewAppPrefix(t, "", tc.prefix)
			owner := a.alice
			if tc.owner != "alice" {
				owner = &models.User{Name: tc.owner, Email: tc.owner + "@example.test", Passwd: "password", IsActive: true}
				if err := models.CreateUser(owner); err != nil {
					t.Fatal(err)
				}
			}
			if tc.owner != "alice" || tc.repo != "project" {
				s := a.holder.Service()
				if _, err := models.CreateHgRepository(context.Background(), s.Runner(), s.RepositoryRoot(), models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: tc.repo}); err != nil {
					t.Fatal(err)
				}
			}
			a.populate(t, 1)
			a.fixture(t, "manifest", "644   "+tc.file+"\n")
			a.fixture(t, "content", "path identity\n")
			base := tc.prefix + "/" + tc.owner + "/" + tc.repo
			w := a.request(t, "GET", base+"/src/"+viewNode+"/"+tc.file, "", "")
			if w.Code != 200 || !strings.Contains(w.Body.String(), "path identity") {
				t.Fatalf("source path misclassified: %d %s", w.Code, w.Body.String())
			}
			w = a.request(t, "GET", base+"/raw/"+viewNode+"/"+tc.file, "", "")
			if w.Code != 200 || w.Body.String() != "path identity\n" {
				t.Fatalf("raw path misclassified: %d %q", w.Code, w.Body.String())
			}
			w = a.request(t, "GET", base+"/commit/"+viewNode, "", "")
			if w.Code != 200 {
				t.Fatalf("changeset path misclassified: %d %s", w.Code, w.Body.String())
			}
			w = a.request(t, "GET", base+"?ref_kind=branch&ref_name=default", "", "")
			if w.Code != 302 || w.Header().Get("Location") != base+"/src/"+viewNode {
				t.Fatalf("landing redirect endpoint: %d %s", w.Code, w.Header().Get("Location"))
			}
			// A decoded slash must still be rejected instead of becoming a path delimiter.
			before := a.calls()
			w = a.request(t, "GET", base+"/src/"+viewNode+"/x%2Fraw%2Ffile.txt", "", "")
			if w.Code != 400 || a.calls() != before {
				t.Fatalf("encoded slash accepted: %d calls %d", w.Code, a.calls()-before)
			}
		})
	}
}
