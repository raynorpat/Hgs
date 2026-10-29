package user_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/collaboration"
	"github.com/gogits/gogs/modules/editordraft"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
	"github.com/macaron-contrib/csrf"
)

func TestEditorDraftHTTPPolicy(t *testing.T) {
	oldURL := setting.AppUrl
	t.Cleanup(func() { setting.AppUrl = oldURL })
	setting.AppUrl = "https://example.test/code/"
	f := newDiscoveryFixture(t, "/code")
	s := editordraft.NewService(models.NewEditorDraftStore(), func(ctx context.Context, a int64, target editordraft.Target) error {
		return models.AuthorizeEditorDraft(ctx, a, target.RepositoryID, target.Kind, target.ItemID)
	}, time.Now)
	f.m.Map(s)
	f.m.Map(collaboration.New(nil))
	f.m.Post("/user/drafts/open", user.DraftOpen)
	f.m.Post("/user/drafts/:page/save", user.DraftSave)
	f.m.Post("/user/drafts/:page/restore", user.DraftRestore)
	f.m.Post("/user/drafts/:page/discard", user.DraftDiscard)
	token := csrf.GenerateToken("disposable-discovery-fixture", "0", "POST")
	call := func(path, actor, body, csrfToken string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://host/code"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Csrf-Token", csrfToken)
		if actor != "" {
			r.SetBasicAuth(actor, "fixture-password")
		}
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		f.m.ServeHTTP(w, r)
		return w
	}
	body := fmt.Sprintf(`{"Target":{"RepositoryID":%d,"Kind":"issue_new","ItemID":0},"CollectionMode":"random"}`, f.repo.Id)
	for _, tc := range []struct {
		actor, token string
		status       int
		code         string
	}{{"", "", 401, "unauthenticated"}, {f.alice.Name, "", 400, "csrf_invalid"}, {f.bob.Name, token, 403, "forbidden"}} {
		w := call("/user/drafts/open", tc.actor, body, tc.token, nil)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Fatalf("policy %d %s", w.Code, w.Body)
		}
	}
	w := call("/user/drafts/open", f.alice.Name, body, token, nil)
	if w.Code != 200 {
		t.Fatalf("open %d %s", w.Code, w.Body)
	}
	var opened struct{ Grant editordraft.Grant }
	if err := json.Unmarshal(w.Body.Bytes(), &opened); err != nil {
		t.Fatal(err)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "hgs_draft" {
			cookie = c
		}
	}
	if cookie == nil || !cookie.Secure || !cookie.HttpOnly || cookie.Path != "/code/user/drafts" || cookie.Domain != "" || cookie.SameSite != http.SameSiteLaxMode || cookie.MaxAge != 1800 {
		t.Fatalf("draft cookie policy: %+v", cookie)
	}
	page := opened.Grant.PageID
	w = call("/user/drafts/"+page+"/restore", f.alice.Name, `{}`, token, cookie)
	if w.Code != 403 {
		t.Fatalf("cookie alone read: %d %s", w.Code, w.Body)
	}
	secret, _ := json.Marshal(map[string]interface{}{"PageSecret": opened.Grant.PageSecret, "Version": 0, "Payload": editordraft.Payload{Body: "private"}})
	w = call("/user/drafts/"+page+"/save", f.alice.Name, string(secret), token, cookie)
	if w.Code != 200 {
		t.Fatalf("save %d %s", w.Code, w.Body)
	}
	w = call("/user/drafts/"+page+"/restore", f.bob.Name, string(secret), token, cookie)
	if w.Code != 403 || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("account switch leak %d %s", w.Code, w.Body)
	}
	disposal, _ := json.Marshal(map[string]string{"DisposalToken": opened.Grant.DisposalToken})
	w = call("/user/drafts/"+page+"/discard", "", string(disposal), "", nil)
	if w.Code != 200 {
		t.Fatalf("expired session disposal %d %s", w.Code, w.Body)
	}
	w = call("/user/drafts/"+page+"/restore", f.alice.Name, string(secret), token, cookie)
	if w.Code != 410 {
		t.Fatalf("discard restore %d %s", w.Code, w.Body)
	}
}

func TestEditorDraftHTTPStorageUnavailable(t *testing.T) {
	f := newDiscoveryFixture(t, "/code")
	f.m.Map(editordraft.NewService(models.NewEditorDraftStore(), func(context.Context, int64, editordraft.Target) error { return models.ErrEditorDraftStorage }, time.Now))
	f.m.Post("/user/drafts/open", user.DraftOpen)
	r := httptest.NewRequest("POST", "http://host/code/user/drafts/open", strings.NewReader(fmt.Sprintf(`{"Target":{"RepositoryID":%d,"Kind":"pull_new"},"CollectionMode":"random"}`, f.repo.Id)))
	r.SetBasicAuth(f.alice.Name, "fixture-password")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Csrf-Token", csrf.GenerateToken("disposable-discovery-fixture", "0", "POST"))
	w := httptest.NewRecorder()
	f.m.ServeHTTP(w, r)
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"save_failed"`) || strings.Contains(w.Body.String(), "forbidden") {
		t.Fatalf("storage error became revocation: %d %s", w.Code, w.Body)
	}
}
