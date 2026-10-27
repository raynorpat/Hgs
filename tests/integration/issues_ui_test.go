//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	apiv1 "github.com/gogits/gogs/modules/auth/apiv1"
	"github.com/gogits/gogs/modules/mailer"
	"github.com/gogits/gogs/modules/setting"
	v1 "github.com/gogits/gogs/routers/api/v1"
	"html"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/auth"
	"github.com/gogits/gogs/modules/middleware"
	"github.com/gogits/gogs/routers/repo"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/binding"
)

func issueFixture(t *testing.T) *uiFixture {
	f := newUIFixture(t, "/code")
	guard := middleware.Toggle(&middleware.ToggleOptions{SignInRequire: true})
	f.m.Get("/issues", guard, user.Issues)
	f.m.Post("/api/v1/markdown", binding.BindIgnErr(apiv1.MarkdownForm{}), v1.Markdown)
	f.m.Group("/:username/:reponame", func() {
		f.m.Get("/issues", repo.Issues)
		f.m.Get("/issues/new", guard, repo.CreateIssue)
		f.m.Post("/issues/new", guard, binding.BindIgnErr(auth.CreateIssueForm{}), repo.CreateIssuePost)
		f.m.Get("/issues/milestones", repo.Milestones)
		f.m.Get("/issues/milestones/new", guard, repo.NewMilestone)
		f.m.Post("/issues/milestones/new", guard, binding.BindIgnErr(auth.CreateMilestoneForm{}), repo.NewMilestonePost)
		f.m.Get("/issues/milestones/:index/edit", guard, repo.UpdateMilestone)
		f.m.Post("/issues/milestones/:index/edit", guard, binding.BindIgnErr(auth.CreateMilestoneForm{}), repo.UpdateMilestonePost)
		f.m.Post("/issues/milestones/:index/:action", guard, repo.UpdateMilestone)
		f.m.Post("/issues/labels/new", guard, binding.BindIgnErr(auth.CreateLabelForm{}), repo.NewLabel)
		f.m.Post("/issues/labels/edit", guard, binding.BindIgnErr(auth.CreateLabelForm{}), repo.UpdateLabel)
		f.m.Post("/issues/labels/delete", guard, repo.DeleteLabel)
		f.m.Get("/issues/:index", repo.ViewIssue)
		f.m.Post("/issues/:index", guard, binding.BindIgnErr(auth.CreateIssueForm{}), repo.UpdateIssue)
		f.m.Post("/issues/:index/label", guard, repo.UpdateIssueLabel)
		f.m.Post("/issues/:index/milestone", guard, repo.UpdateIssueMilestone)
		f.m.Post("/issues/:index/assignee", guard, repo.UpdateAssignee)
		f.m.Get("/issues/:index/attachment/:id", guard, repo.IssueGetAttachment)
		f.m.Post("/comment/:action", guard, repo.Comment)
	}, middleware.RepoAssignment(true))
	return f
}
func TestSharedUIIssuesWorkflow(t *testing.T) {
	f := issueFixture(t)
	root := "/fixtureowner/empty-lab"
	// Issues need only metadata: make the empty store unavailable to prove no
	// history/open command is required by any form or list/detail render.
	store, err := models.RepositoryStoragePath(setting.RepoRootPath, f.empty.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(store, store+".unavailable"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(store+".unavailable", store)
	captured, restore := mailer.CaptureIntegrationMail()
	defer restore()
	setting.Service.EnableNotifyMail = true
	if err := models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.empty.Id, Mode: models.WRITABLE}); err != nil {
		t.Fatal(err)
	}
	milestonePost := f.post(t, root+"/issues/milestones/new", "owner", url.Values{"title": {"First delivery"}, "content": {"**Milestone**"}, "due_date": {"2027-04-03"}}, true)
	if milestonePost.Code != 302 {
		t.Fatalf("milestone create %d %s", milestonePost.Code, milestonePost.Body.String())
	}
	milestone, err := models.GetMilestoneByIndex(f.empty.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if milestone.Deadline.UTC().Format("2006-01-02") != "2027-04-03" {
		t.Fatal("native due date lost")
	}
	labelPost := f.post(t, root+"/issues/labels/new", "owner", url.Values{"title": {"Arbitrary label"}, "color": {"#f0d2ab"}}, true)
	if labelPost.Code != 302 {
		t.Fatal("label create", labelPost.Code, labelPost.Body.String())
	}
	labels, err := models.GetLabels(f.empty.Id)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels %v %v", labels, err)
	}
	requireUI(t, f.get(t, root+"/issues/new", "owner"), f.prefix+root+"/issues")
	german := f.get(t, root+"/issues/new?lang=de-DE", "owner").Body.String()
	for _, want := range []string{"Aufgabe erstellen", "Vorschau", "Skip to content", "Back to workspace"} {
		if !strings.Contains(german, want) {
			t.Errorf("German locale/fallback missing %q", want)
		}
	}
	if strings.Contains(german, ">skip_content<") || strings.Contains(german, ">create_issue<") {
		t.Fatal("raw locale keys rendered")
	}

	w := f.post(t, root+"/issues/new", "owner", url.Values{"ui": {"1"}, "title": {"Shared workflow"}, "content": {"**body** <script>unsafe()</script> @fixturereader"}, "milestoneid": {fmt.Sprint(milestone.Id)}, "assigneeid": {fmt.Sprint(f.reader.Id)}}, true)
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), root+"/issues/1") {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	issue, err := models.GetIssueByIndex(f.empty.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	if issue.AssigneeId != f.reader.Id || issue.MilestoneId != milestone.Id {
		t.Fatal("creation metadata lost")
	}
	select {
	case msg := <-captured:
		if len(msg.To) != 1 || msg.To[0] != f.reader.Email || !strings.Contains(msg.Body, "/issues/1") {
			t.Fatalf("mention mail %v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("mention mail not captured")
	}
	if err := models.WatchRepo(f.reader.Id, f.empty.Id, true); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		path   string
		values url.Values
	}{
		{"/issues/1", url.Values{"ui": {"1"}, "title": {"Edited workflow"}, "content": {"edited **body**"}}},
		{"/issues/1/label", url.Values{"ui": {"1"}, "id": {fmt.Sprint(labels[0].Id)}, "action": {"attach"}}},
		{"/issues/1/assignee", url.Values{"ui": {"1"}, "issue": {fmt.Sprint(issue.Id)}, "assigneeid": {"0"}}},
		{"/issues/1/milestone", url.Values{"ui": {"1"}, "issue": {fmt.Sprint(issue.Id)}, "milestoneid": {"0"}}},
		{"/issues/1/milestone", url.Values{"ui": {"1"}, "issue": {fmt.Sprint(issue.Id)}, "milestoneid": {fmt.Sprint(milestone.Id)}}},
	} {
		if w := f.post(t, root+mutation.path, "owner", mutation.values, true); w.Code != 303 {
			t.Fatalf("mutation %s %d %s", mutation.path, w.Code, w.Body.String())
		}
	}
	issue, err = models.GetIssueById(issue.Id)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Name != "Edited workflow" || issue.Content != "edited **body**" || issue.AssigneeId != 0 || !strings.Contains(issue.LabelIds, fmt.Sprint(labels[0].Id)) {
		t.Fatalf("metadata/edit not persisted %#v", issue)
	}
	requireUI(t, f.get(t, root+"/issues/1", "owner"), f.prefix+root+"/issues")
	if b := f.get(t, root+"/issues/1", "owner").Body.String(); strings.Contains(b, "<script>unsafe()") {
		t.Fatal("unsafe saved body")
	}
	for _, action := range []string{"Close", "Reopen"} {
		w = f.post(t, root+"/comment/new", "owner", url.Values{"ui": {"1"}, "issueIndex": {"1"}, "content": {"comment " + action}, "change_status": {action}}, true)
		if w.Code != 303 {
			t.Fatalf("comment %d: %s", w.Code, w.Body.String())
		}
	}
	milestone, err = models.GetMilestoneById(milestone.Id)
	if err != nil || milestone.Completeness != 0 || milestone.NumClosedIssues != 0 {
		t.Fatalf("reopened milestone progress stale: %#v %v", milestone, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case msg := <-captured:
			if len(msg.To) != 1 || msg.To[0] != f.reader.Email {
				t.Fatalf("watcher mail %v", msg)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("watcher mail not captured")
		}
	}
	comments, err := models.GetIssueComments(issue.Id)
	if err != nil || len(comments) != 4 {
		t.Fatalf("history=%v err=%v", comments, err)
	}
	for _, c := range comments {
		if c.Type == 0 && !strings.Contains(f.get(t, root+"/issues/1", "owner").Body.String(), fmt.Sprintf("issue-comment-%d", c.Id)) {
			t.Fatal("missing comment anchor")
		}
	}
	bad := f.post(t, root+"/issues/new", "owner", url.Values{"ui": {"1"}, "title": {""}, "content": {"preserved body"}}, true)
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "preserved body") {
		t.Fatalf("validation did not preserve input: %d %s", bad.Code, bad.Body.String())
	}
	if w := f.post(t, root+"/issues/1", "owner", url.Values{"ui": {"1"}, "title": {""}, "content": {"preserved edit"}}, true); w.Code != 400 || !strings.Contains(w.Body.String(), "preserved edit") {
		t.Fatal("edit input not preserved")
	}
	for _, path := range []string{root + "/issues", root + "/issues?state=closed", root + "/issues?labels=999999", root + "/issues/milestones", root + "/issues/milestones/new", root + "/issues/milestones/1/edit", "/issues"} {
		w := f.get(t, path, "owner")
		requireUI(t, w, "")
		for _, old := range []string{"bootstrap", "/js/app.js", "data-toggle="} {
			if strings.Contains(w.Body.String(), old) {
				t.Errorf("legacy dependency %s in %s", old, path)
			}
		}
	}
	if w := f.post(t, root+"/issues/milestones/1/edit", "owner", url.Values{"title": {"Revised milestone"}, "content": {"revised"}, "due_date": {"not-a-date"}}, true); w.Code != 400 || !strings.Contains(w.Body.String(), "revised") {
		t.Fatal("invalid date was not safely preserved", w.Code)
	}
	if w := f.post(t, root+"/issues/milestones/1/close", "owner", url.Values{}, false); w.Code != 400 {
		t.Fatal("milestone CSRF bypass")
	}
	if w := f.post(t, root+"/issues/milestones/1/close", "owner", url.Values{}, true); w.Code != 302 {
		t.Fatal("milestone close", w.Code)
	}
	milestone, err = models.GetMilestoneById(milestone.Id)
	if err != nil || !milestone.IsClosed {
		t.Fatal("milestone close not saved")
	}
	if w := f.post(t, root+"/issues/milestones/1/open", "owner", url.Values{}, true); w.Code != 302 {
		t.Fatal("milestone reopen", w.Code)
	}
	refreshed, err := models.GetRepositoryById(f.empty.Id)
	if err != nil || refreshed.NumClosedMilestones != 0 {
		t.Fatal("reopened milestone repository count stale")
	}
	if w := f.get(t, root+"/issues/milestones/1/open", "owner"); w.Code == 200 {
		t.Fatal("GET milestone mutation retained")
	}
	if w := f.post(t, root+"/issues/new", "owner", url.Values{"title": {"CSRF blocked"}}, false); w.Code != 400 {
		t.Fatal("create CSRF bypass")
	}

	if w := f.post(t, root+"/issues/labels/edit", "owner", url.Values{"id": {fmt.Sprint(labels[0].Id)}, "title": {"Updated label"}, "color": {"#123456"}}, true); w.Code != 302 {
		t.Fatal("label edit", w.Code)
	}
	updatedLabel, err := models.GetLabelById(labels[0].Id)
	if err != nil || updatedLabel.Name != "Updated label" || updatedLabel.Color != "#123456" {
		t.Fatal("label edit did not persist")
	}
	if w := f.post(t, root+"/issues/labels/delete", "owner", url.Values{"ui": {"1"}, "remove": {fmt.Sprint(labels[0].Id)}}, true); w.Code != 303 {
		t.Fatal("label delete", w.Code)
	}
	if _, err := models.GetLabelById(labels[0].Id); err != models.ErrLabelNotExist {
		t.Fatal("label deletion failed")
	}
	if w := f.post(t, root+"/issues/milestones/1/delete", "owner", url.Values{}, true); w.Code != 302 {
		t.Fatal("milestone delete", w.Code)
	}
	cleared, err := models.GetIssueById(issue.Id)
	if err != nil || cleared.MilestoneId != 0 {
		t.Fatal("milestone delete did not clear assignment")
	}

}
func TestSharedUIIssuePrivateAttachments(t *testing.T) {
	f := issueFixture(t)
	oldEnabled, oldPath, oldTypes, oldCount := setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles
	setting.AttachmentEnabled = true
	setting.AttachmentPath = filepath.Join(f.root, "attachments")
	setting.AttachmentAllowedTypes = "*/*"
	setting.AttachmentMaxFiles = 10
	defer func() {
		setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles = oldEnabled, oldPath, oldTypes, oldCount
	}()
	if err := os.MkdirAll(setting.AttachmentPath, 0700); err != nil {
		t.Fatal(err)
	}
	root := "/fixtureowner/private-lab"
	w := f.issueUpload(t, root+"/issues/new", url.Values{"ui": {"1"}, "title": {"private attachment"}, "content": {"secret body"}}, "private.txt", "private attachment bytes")
	if w.Code != 303 {
		t.Fatalf("multipart create %d %s", w.Code, w.Body.String())
	}
	issue, err := models.GetIssueByIndex(f.private.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	attachments := issue.Attachments()
	if len(attachments) != 1 {
		t.Fatalf("attachments=%v", attachments)
	}
	path := fmt.Sprintf("%s/issues/1/attachment/%d", root, attachments[0].Id)
	for _, who := range []string{"owner", "reader"} {
		if w := f.get(t, path, who); w.Code != 200 || w.Body.String() != "private attachment bytes" {
			t.Fatalf("authorized attachment %s %d %s", who, w.Code, w.Body.String())
		}
	}
	if w := f.get(t, path, ""); w.Code != 404 || strings.Contains(w.Body.String(), "private attachment bytes") {
		t.Fatal("private anonymous attachment leaked")
	}
	if w := f.get(t, fmt.Sprintf("/fixtureowner/empty-lab/issues/1/attachment/%d", attachments[0].Id), "owner"); w.Code != 404 {
		t.Fatal("attachment rebound to wrong repository")
	}
	if err := models.DeleteAccess(&models.Access{UserId: f.reader.Id, RepoId: f.private.Id}); err != nil {
		t.Fatal(err)
	}
	if w := f.get(t, path, "reader"); w.Code != 404 {
		t.Fatal("revoked attachment access")
	}
	w = f.issueUpload(t, root+"/comment/new", url.Values{"ui": {"1"}, "issueIndex": {"1"}, "content": {""}}, "comment.txt", "comment attachment bytes")
	if w.Code != 303 || !strings.Contains(w.Header().Get("Location"), "#issue-comment-") {
		t.Fatalf("attachment-only comment %d %s", w.Code, w.Header().Get("Location"))
	}
	comments, err := models.GetIssueComments(issue.Id)
	if err != nil || len(comments) != 1 || len(comments[0].Attachments()) != 1 {
		t.Fatal("comment attachment not linked")
	}
	zero := f.issueUpload(t, root+"/comment/new", url.Values{"ui": {"1"}, "issueIndex": {"1"}}, "empty.txt", "")
	if zero.Code != 303 {
		t.Fatal("empty file comment", zero.Code, zero.Body.String())
	}
	comments, err = models.GetIssueComments(issue.Id)
	if err != nil || len(comments) != 2 {
		t.Fatal("empty file comment not created")
	}
	zeroFiles := comments[1].Attachments()
	if len(zeroFiles) != 1 {
		t.Fatal("empty file not attached")
	}
	emptyURL := fmt.Sprintf("%s/issues/1/attachment/%d", root, zeroFiles[0].Id)
	if w := f.get(t, emptyURL, "owner"); w.Code != 200 || w.Body.Len() != 0 {
		t.Fatal("zero-byte attachment changed", w.Code, w.Body.Len())
	}
	setting.AttachmentMaxFiles = 0
	w = f.issueUpload(t, root+"/issues/new", url.Values{"ui": {"1"}, "title": {"Rejected attachment"}, "content": {"preserved attachment body"}}, "private.txt", "private bytes")
	if w.Code != 400 || !strings.Contains(w.Body.String(), "preserved attachment body") {
		t.Fatal("upload error discarded input", w.Code)
	}
	if _, err = models.GetIssueByIndex(f.private.Id, 2); err != models.ErrIssueNotExist {
		t.Fatal("invalid upload created issue")
	}
}
func (f *uiFixture) issueUpload(t *testing.T, path string, values url.Values, name, content string) *httptest.ResponseRecorder {
	t.Helper()
	page := f.get(t, "/repo/create", "owner")
	token := regexp.MustCompile(`name="_csrf" value="([^"]+)"`).FindStringSubmatch(page.Body.String())
	if len(token) != 2 {
		t.Fatal("missing csrf")
	}
	values.Set("_csrf", html.UnescapeString(token[1]))
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for key, list := range values {
		for _, value := range list {
			writer.WriteField(key, value)
		}
	}
	file, err := writer.CreateFormFile("attachments", name)
	if err != nil {
		t.Fatal(err)
	}
	file.Write([]byte(content))
	writer.Close()
	req := httptest.NewRequest("POST", f.prefix+path, &buf)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Fixture-User", "owner")
	for _, cookie := range page.Result().Cookies() {
		req.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, middleware.WithBrowserResponseController(req, w))
	return w
}

func TestSharedUIIssueGlobalFilters(t *testing.T) {
	f := issueFixture(t)
	issue := &models.Issue{RepoId: f.private.Id, Index: 1, Name: "revoked private issue", PosterId: f.reader.Id, AssigneeId: f.reader.Id}
	if err := models.NewIssue(issue); err != nil {
		t.Fatal(err)
	}
	if err := models.NewIssueUserPairs(f.private.Id, issue.Id, f.owner.Id, f.reader.Id, f.reader.Id); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Where("user_id=? AND repo_id=?", f.reader.Id, f.private.Id).Delete(new(models.Access)); err != nil {
		t.Fatal(err)
	}
	// More than one page of denied candidates must not displace authorized rows.
	for i := 0; i < 25; i++ {
		hidden := &models.Issue{RepoId: f.private.Id, Index: int64(i + 2), Name: fmt.Sprintf("hidden-%d", i), PosterId: f.reader.Id, AssigneeId: f.reader.Id}
		if err := models.NewIssue(hidden); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 21; i++ {
		visible := &models.Issue{RepoId: f.public.Id, Index: int64(i + 1), Name: fmt.Sprintf("visible-%d", i), PosterId: f.reader.Id, AssigneeId: f.reader.Id}
		if err := models.NewIssue(visible); err != nil {
			t.Fatal(err)
		}
	}
	result, err := models.ListUIIssues(context.Background(), models.UIIssueQuery{ActorID: f.reader.Id, Global: true, View: "assigned", Page: 2})
	if err != nil || result.Total != 21 || len(result.Issues) != 1 || result.Stats.AssignCount != 21 || result.Stats.CreateCount != 21 || result.Stats.AllCount != 0 {
		t.Fatalf("policy count/page %#v %v", result, err)
	}
	for _, query := range []string{"?type=assigned", "?type=created_by", fmt.Sprintf("?repoid=%d", f.private.Id)} {
		w := f.get(t, "/issues"+query, "reader")
		requireUI(t, w, f.prefix+"/issues")
		if strings.Contains(w.Body.String(), issue.Name) {
			t.Fatal("global list disclosed revoked issue")
		}
	}
	if _, err := f.db.Id(f.public.Id).Cols("state").Update(&models.Repository{State: models.RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	unavailable, err := models.ListUIIssues(context.Background(), models.UIIssueQuery{ActorID: f.reader.Id, Global: true, View: "assigned", Page: 1})
	if err != nil || unavailable.Total != 0 || unavailable.Stats.AssignCount != 0 {
		t.Fatal("unavailable repositories entered aggregates", unavailable, err)
	}

}

func TestSharedUIIssueMutationBoundaries(t *testing.T) {
	f := issueFixture(t)
	root := "/fixtureowner/empty-lab"
	local := &models.Issue{RepoId: f.empty.Id, Index: 1, Name: "author issue", PosterId: f.reader.Id, Content: "original"}
	private := &models.Issue{RepoId: f.private.Id, Index: 1, Name: "private unchanged", PosterId: f.owner.Id, Content: "private content"}
	for _, i := range []*models.Issue{local, private} {
		if err := models.NewIssue(i); err != nil {
			t.Fatal(err)
		}
	}
	label := &models.Label{RepoId: f.private.Id, Name: "private label", Color: "#ffffff"}
	if err := models.NewLabel(label); err != nil {
		t.Fatal(err)
	}
	milestone := &models.Milestone{RepoId: f.private.Id, Index: 1, Name: "private milestone", Deadline: time.Now()}
	if err := models.NewMilestone(milestone); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		who, path string
		values    url.Values
		status    int
	}{
		{"owner", "/issues/1/label", url.Values{"id": {fmt.Sprint(label.Id)}, "action": {"attach"}}, 404},
		{"owner", "/issues/1/milestone", url.Values{"issue": {fmt.Sprint(private.Id)}, "milestoneid": {"0"}}, 404},
		{"owner", "/issues/1/assignee", url.Values{"issue": {fmt.Sprint(private.Id)}, "assigneeid": {"0"}}, 404},
		{"owner", "/issues/1/milestone", url.Values{"issue": {fmt.Sprint(local.Id)}, "milestoneid": {fmt.Sprint(milestone.Id)}}, 404},
		{"owner", "/issues/labels/edit", url.Values{"id": {fmt.Sprint(label.Id)}, "title": {"changed"}, "color": {"#000000"}}, 404},
		{"owner", "/issues/labels/delete", url.Values{"remove": {fmt.Sprint(label.Id)}}, 404},
		{"reader", "/issues/labels/new", url.Values{"title": {"unauthorized"}, "color": {"#000000"}}, 403},
		{"reader", "/issues/milestones/new", url.Values{"title": {"unauthorized"}}, 403},
		{"reader", "/issues/1/assignee", url.Values{"issue": {fmt.Sprint(local.Id)}, "assigneeid": {"0"}}, 403},
	} {
		w := f.post(t, root+tc.path, tc.who, tc.values, true)
		if w.Code != tc.status {
			t.Errorf("%s %s=%d want %d body=%s", tc.who, tc.path, w.Code, tc.status, w.Body.String())
		}
	}
	p, err := models.GetIssueById(private.Id)
	if err != nil || p.Name != private.Name || p.Content != private.Content {
		t.Fatal("foreign issue changed")
	}
	l, err := models.GetLabelById(label.Id)
	if err != nil || l.Name != label.Name {
		t.Fatal("foreign label changed")
	}
	if w := f.post(t, root+"/issues/1", "reader", url.Values{"ui": {"1"}, "title": {"author edit"}, "content": {"author allowed"}}, true); w.Code != 303 {
		t.Fatal("author edit denied", w.Code)
	}
	if w := f.post(t, "/fixtureowner/private-lab/issues/1", "reader", url.Values{"title": {"forbidden"}, "content": {"forbidden"}}, true); w.Code != 403 {
		t.Fatal("non-author edit permitted", w.Code)
	}
}
func TestSharedUIIssueBrowserFixture(t *testing.T) {
	addr := os.Getenv("UI_ISSUE_FIXTURE_ADDR")
	if addr == "" {
		t.Skip("opt-in disposable Issues browser fixture")
	}
	f := issueFixture(t)
	oldEnabled, oldPath, oldTypes, oldCount := setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles
	setting.AttachmentEnabled = true
	setting.AttachmentPath = filepath.Join(f.root, "attachments")
	setting.AttachmentAllowedTypes = "*/*"
	setting.AttachmentMaxFiles = 10
	t.Cleanup(func() {
		setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles = oldEnabled, oldPath, oldTypes, oldCount
	})
	os.MkdirAll(setting.AttachmentPath, 0700)
	for _, r := range []*models.Repository{f.public, f.private, f.empty} {
		if w := f.post(t, "/fixtureowner/"+r.Name+"/issues/new", "owner", url.Values{"ui": {"1"}, "title": {"Investigate a long issue title across narrow screens and metadata controls"}, "content": {"# Discussion\nA **real** issue with a long paragraph.\n\n<script>window.fixtureUnsafe=true</script>\n\n" + strings.Repeat("Long discussion content ", 60)}}, true); w.Code != 303 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	root := "/fixtureowner/" + f.public.Name
	f.post(t, root+"/issues/labels/new", "owner", url.Values{"title": {"A label with a long readable name"}, "color": {"#f0d2ab"}}, true)
	f.post(t, root+"/issues/milestones/new", "owner", url.Values{"title": {"First delivery"}, "content": {"A **real** milestone"}, "due_date": {"2027-04-03"}}, true)
	f.post(t, root+"/comment/new", "owner", url.Values{"ui": {"1"}, "issueIndex": {"1"}, "content": {"A real **comment**"}, "change_status": {"Close"}}, true)
	f.post(t, root+"/comment/new", "owner", url.Values{"ui": {"1"}, "issueIndex": {"1"}, "content": {"Reopened for browser checks"}, "change_status": {"Reopen"}}, true)
	server := &http.Server{Addr: addr, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.m.ServeHTTP(w, middleware.WithBrowserResponseController(r, w))
	})}
	f.m.Get("/fixture/stop", func(ctx *middleware.Context) {
		ctx.Write([]byte("stopping"))
		go server.Shutdown(context.Background())
	})
	t.Logf("disposable Issues fixture http://%s/code/fixture/account/owner", addr)
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
		t.Fatal(err)
	}
}
