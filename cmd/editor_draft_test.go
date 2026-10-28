package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestEditorDraftHTTPPolicy(t *testing.T) {
	a := newViewApp(t)
	w := a.request(t, "GET", "/gogs/user/drafts/session", "", "")
	if w.Code != 401 || draftResponseCode(w) != "unauthenticated" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("anonymous draft policy: %d %s", w.Code, w.Body.String())
	}
	b := a.browser(t, "alice")
	b.cookies["_csrf"] = &http.Cookie{Name: "_csrf", Value: "expired-cookie"}
	w = b.do(t, "GET", "/gogs/user/drafts/session", nil)
	var state struct {
		Code, CSRF string
		ActorID    int64
	}
	if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || state.ActorID != a.alice.Id || state.CSRF == "" || state.CSRF == "expired-cookie" {
		t.Fatalf("fresh state: %+v status %d", state, w.Code)
	}
	cookieToken, err := url.QueryUnescape(b.cookies["_csrf"].Value)
	if err != nil || cookieToken != state.CSRF {
		t.Fatal("response CSRF cookie differs from returned fresh token")
	}
	for _, tc := range []struct {
		name, account, token string
		cookies              bool
		status               int
		code                 string
	}{
		{"expired-session", fmt.Sprint(a.alice.Id), state.CSRF, false, 401, "unauthenticated"},
		{"changed-account", fmt.Sprint(a.bob.Id), state.CSRF, true, 403, "account_changed"},
		{"expired-csrf", fmt.Sprint(a.alice.Id), "expired-cookie", true, 400, "csrf_invalid"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "http://host/gogs/alice/project/issues/new", strings.NewReader("ui=1&title=Must+not+save&content=retained"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("X-Hgs-Editor-Submit", "1")
			r.Header.Set("X-Hgs-Editor-Account", tc.account)
			r.Header.Set("X-Csrf-Token", tc.token)
			if tc.cookies {
				for _, c := range b.cookies {
					r.AddCookie(c)
				}
			}
			w := httptest.NewRecorder()
			a.handler.ServeHTTP(w, r)
			if w.Code != tc.status || draftResponseCode(w) != tc.code {
				t.Fatalf("submit policy: %d %s", w.Code, w.Body)
			}
			if w.Header().Get("Location") != "" {
				t.Fatal("editor navigated to login")
			}
		})
	}
}

func TestEditorDraftPurgerShutdown(t *testing.T) {
	started, exited := make(chan struct{}), make(chan struct{})
	stop := startDraftPurger(func(ctx context.Context) error { close(started); <-ctx.Done(); close(exited); return ctx.Err() }, func() bool { return true })
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("purger did not start")
	}
	stop()
	select {
	case <-exited:
	default:
		t.Fatal("stop returned before purger exited")
	}
}

func TestEditorDraftEditorOptIn(t *testing.T) {
	a := newViewApp(t)
	for _, path := range []string{"/gogs/alice/project/issues/new", "/gogs/alice/project/pulls/new"} {
		w := a.request(t, "GET", path, "", "alice")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "data-editor-draft=") || !strings.Contains(w.Body.String(), "data-draft-cancel") || !strings.Contains(w.Body.String(), "/gogs/js/app/drafts.js") {
			t.Fatalf("missing editor recovery UI on %s: status %d", path, w.Code)
		}
	}
}

func draftResponseCode(w *httptest.ResponseRecorder) string {
	var v struct{ Code string }
	json.Unmarshal(w.Body.Bytes(), &v)
	return v.Code
}

func editorPost(t *testing.T, b *viewBrowser, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("POST", "http://host"+path, strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-Hgs-Editor-Submit", "1")
	r.Header.Set("X-Hgs-Editor-Account", fmt.Sprint(b.a.alice.Id))
	r.Header.Set("X-Csrf-Token", b.token)
	for _, c := range b.cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	b.a.handler.ServeHTTP(w, r)
	return w
}
func TestEditorDraftCanonicalSubmitOutcomes(t *testing.T) {
	a := newViewApp(t)
	b := a.browser(t, "alice")
	b.do(t, "GET", "/gogs/alice/project/issues/new", nil)
	w := editorPost(t, b, "/gogs/alice/project/issues/new", url.Values{"ui": {"1"}, "content": {"retained"}})
	if w.Code != 400 || draftResponseCode(w) != "validation" {
		t.Fatalf("validation: %d %s", w.Code, w.Body)
	}
	w = editorPost(t, b, "/gogs/alice/project/issues/new", url.Values{"ui": {"1"}, "title": {strings.Repeat("😀", 255)}, "content": {"saved once"}})
	if w.Code != 200 || draftResponseCode(w) != "saved" {
		t.Fatalf("success: %d %s", w.Code, w.Body)
	}
	var result struct {
		Destination string
		Committed   bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Committed || result.Destination != "/gogs/alice/project/issues/1" || w.Header().Get("Location") != "" {
		t.Fatalf("canonical result: %+v", result)
	}
	issue, err := models.GetIssueByIndex(a.repo.Id, 1)
	if err != nil || issue.Content != "saved once" {
		t.Fatal("canonical mutation missing", err)
	}
	w = editorPost(t, b, "/gogs/alice/project/comment/new", url.Values{"ui": {"1"}, "issueIndex": {"1"}, "content": {"close comment"}, "change_status": {"Close"}})
	if w.Code != 200 || draftResponseCode(w) != "saved" {
		t.Fatalf("submitter: %d %s", w.Code, w.Body)
	}
	issue, err = models.GetIssueByIndex(a.repo.Id, 1)
	if err != nil || !issue.IsClosed || issue.NumComments != 1 {
		t.Fatalf("submitter lost: %+v %v", issue, err)
	}
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("DROP TABLE watch"); err != nil {
		t.Fatal(err)
	}
	w = editorPost(t, b, "/gogs/alice/project/issues/new", url.Values{"ui": {"1"}, "title": {"partially committed"}, "content": {"body already saved"}})
	if w.Code != 200 || draftResponseCode(w) != "partial" {
		t.Fatalf("partial outcome: %d %s", w.Code, w.Body)
	}
	if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if !result.Committed || result.Destination != "/gogs/alice/project/issues/2" {
		t.Fatalf("partial guidance: %+v", result)
	}
	if _, err = models.GetIssueByIndex(a.repo.Id, 2); err != nil {
		t.Fatal("committed result missing", err)
	}
}

func draftFixtureDB(t *testing.T) *xorm.Engine {
	t.Helper()
	db, err := xorm.NewEngine("sqlite3", "file:"+models.DbCfg.Path+"?cache=shared&mode=rwc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func TestEditorDraftRejectedSessionIdentity(t *testing.T) {
	for _, mode := range []string{"disabled", "deleted", "outage", "expired", "natural-expiry"} {
		t.Run(mode, func(t *testing.T) {
			a := newViewApp(t)
			if mode == "natural-expiry" {
				old := setting.SessionConfig
				t.Cleanup(func() { setting.SessionConfig = old })
				setting.SessionConfig.Maxlifetime, setting.SessionConfig.Gclifetime = 1, 1
				a.handler = newWebHandler(a.holder)
			}
			b := a.browser(t, "alice")
			db := draftFixtureDB(t)
			want, code := 403, "forbidden"
			var err error
			switch mode {
			case "disabled":
				_, err = db.Id(a.alice.Id).Cols("is_active").Update(&models.User{IsActive: false})
			case "deleted":
				_, err = db.Id(a.alice.Id).Delete(new(models.User))
			case "outage":
				_, err = db.Exec("ALTER TABLE user RENAME TO unavailable_user")
				want, code = 503, "save_failed"
			case "expired":
				b.cookies = map[string]*http.Cookie{}
				want, code = 401, "unauthenticated"
			case "natural-expiry":
				time.Sleep(3 * time.Second)
				want, code = 401, "unauthenticated"
			}
			if err != nil {
				t.Fatal(err)
			}
			w := b.do(t, "GET", "/gogs/user/drafts/session", nil)
			if w.Code != want || draftResponseCode(w) != code {
				t.Fatalf("%s: %d %s", mode, w.Code, w.Body)
			}
		})
	}
}
func TestEditorDraftPullCandidateValidation(t *testing.T) {
	a := newViewApp(t)
	b := a.browser(t, "alice")
	b.do(t, "GET", "/gogs/alice/project/pulls/new", nil)
	w := editorPost(t, b, "/gogs/alice/project/pulls/new", url.Values{"title": {"keep title"}, "content": {"keep text"}, "source_repository": {"misspelled/missing"}})
	if draftResponseCode(w) != "validation" {
		t.Fatalf("candidate typo erased editor: %d %s", w.Code, w.Body)
	}
	db := draftFixtureDB(t)
	source := &models.Repository{OwnerId: a.bob.Id, Name: "private-source", LowerName: "private-source", IsPrivate: true, State: models.RepositoryReady}
	if _, err := db.Insert(source); err != nil {
		t.Fatal(err)
	}
	w = editorPost(t, b, "/gogs/alice/project/pulls/new", url.Values{"title": {"keep title"}, "content": {"keep text"}, "source_repository": {"bob/private-source"}})
	if draftResponseCode(w) != "validation" {
		t.Fatalf("inaccessible candidate disclosed or erased: %d %s", w.Code, w.Body)
	}
	if _, err := db.Id(a.repo.Id).Cols("state").Update(&models.Repository{State: models.RepositoryDeleting}); err != nil {
		t.Fatal(err)
	}
	w = editorPost(t, b, "/gogs/alice/project/pulls/new", url.Values{"source_repository": {"misspelled/missing"}})
	if draftResponseCode(w) != "forbidden" {
		t.Fatalf("target loss classified as candidate input: %d %s", w.Code, w.Body)
	}
}

func TestEditorDraftStatusOnlyCheck(t *testing.T) {
	a := newViewApp(t)
	b := a.browser(t, "alice")
	b.do(t, "GET", "/gogs/alice/project/issues/new", nil)
	db := draftFixtureDB(t)
	for _, tc := range []struct {
		body, token, code string
		status            int
	}{
		{fmt.Sprintf(`{"Target":{"RepositoryID":%d,"Kind":"issue_new"}}`, a.repo.Id), b.token, "ok", 200},
		{fmt.Sprintf(`{"Target":{"RepositoryID":%d,"Kind":"issue_new"}}`, a.repo.Id), "", "csrf_invalid", 400},
		{fmt.Sprintf(`{"Target":{"RepositoryID":%d,"Kind":"pull_new"},"Metadata":{"source_repository":"absent/source"}}`, a.repo.Id), b.token, "invalid_payload", 422},
		{fmt.Sprintf(`{"Target":{"RepositoryID":%d,"Kind":"pull_new"},"Title":"not allowed"}`, a.repo.Id), b.token, "invalid_payload", 422},
	} {
		r := httptest.NewRequest("POST", "http://host/gogs/user/drafts/check", strings.NewReader(tc.body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Csrf-Token", tc.token)
		for _, c := range b.cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		a.handler.ServeHTTP(w, r)
		if w.Code != tc.status || draftResponseCode(w) != tc.code {
			t.Errorf("status check: %d %s", w.Code, w.Body)
		}
		var response map[string]interface{}
		json.Unmarshal(w.Body.Bytes(), &response)
		if len(response) != 1 {
			t.Errorf("status endpoint exposed more than code: %v", response)
		}
	}
	if n, err := db.Count(new(models.EditorDraft)); err != nil || n != 0 {
		t.Fatalf("status check allocated drafts: %d %v", n, err)
	}
}
func TestEditorDraftPullSourceRevocation(t *testing.T) {
	a := newViewApp(t)
	db := draftFixtureDB(t)
	source := &models.Repository{OwnerId: a.bob.Id, Name: "private-source", LowerName: "private-source", IsPrivate: true, State: models.RepositoryReady}
	if _, err := db.Insert(source); err != nil {
		t.Fatal(err)
	}
	p := &models.PullRequest{AuthorID: a.alice.Id, TargetRepoID: a.repo.Id, SourceRepoID: source.Id, Number: 1, State: models.PullOpen, Title: "existing"}
	if _, err := db.Insert(p); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Insert(&models.Access{UserId: a.alice.Id, RepoId: source.Id, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	if _, err := models.GetPullRecord(context.Background(), a.alice.Id, a.repo.Id, 1); err != nil {
		t.Fatal("initial source permission", err)
	}
	if _, err := db.Where("user_id = ? AND repo_id = ?", a.alice.Id, source.Id).Delete(new(models.Access)); err != nil {
		t.Fatal(err)
	}
	b := a.browser(t, "alice")
	b.do(t, "GET", "/gogs/alice/project/pulls/new", nil)
	for _, suffix := range []string{"edit", "comments", "review"} {
		w := editorPost(t, b, "/gogs/alice/project/pulls/1/"+suffix, url.Values{"title": {"keep"}, "content": {"sensitive"}, "state": {"approved"}, "comparison_id": {"1"}})
		if draftResponseCode(w) != "forbidden" {
			t.Errorf("%s source denial: %d %s", suffix, w.Code, w.Body)
		}
	}
	w := b.do(t, "POST", "/gogs/alice/project/pulls/1/comments", url.Values{"content": {"native"}})
	if w.Code != 404 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("native branch changed: %d %s", w.Code, w.Body)
	}
}
