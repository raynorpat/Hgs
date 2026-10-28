package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
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
