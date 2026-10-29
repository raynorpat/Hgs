package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/editordraft"
	"github.com/gogits/gogs/modules/setting"
	"github.com/gogits/gogs/routers/user"
)

func TestEditorDraftFixtureDropDiscardOptions(t *testing.T) {
	if _, err := parseDraftFixtureOptions("", "", "drop-discard"); err != nil {
		t.Fatal("drop-discard mode is unavailable")
	}
	if _, err := parseDraftFixtureOptions("", "", "drop-discard,save-outage"); err == nil {
		t.Fatal("combined failure modes were accepted")
	}
}

func TestEditorDraftFixtureDropDiscardTransportScope(t *testing.T) {
	page := strings.Repeat("a", 64)
	for _, prefix := range []string{"", "/draft-discard"} {
		t.Run("prefix="+prefix, func(t *testing.T) {
			var calls atomic.Int32
			base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("X-Native-Fixture", "unchanged")
				w.WriteHeader(http.StatusAccepted)
				w.Write([]byte("native response"))
			})
			server := httptest.NewServer(draftFixtureHandler(base, draftFixtureOptions{prefix: prefix, failure: "drop-discard"}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 3 * time.Second
			for _, tc := range []struct {
				method, path string
				drop         bool
			}{
				{"GET", prefix + "/user/drafts/" + page + "/discard", false},
				{"POST", prefix + "/user/drafts/" + page + "/discard", true},
				{"DELETE", prefix + "/user/drafts/" + page + "/discard", false},
				{"POST", "/wrong" + prefix + "/user/drafts/" + page + "/discard", false},
				{"POST", prefix + "/user/drafts/short/discard", false},
				{"POST", prefix + "/user/drafts/" + strings.Repeat("A", 64) + "/discard", false},
				{"POST", prefix + "/user/drafts/" + page + "/extra/discard", false},
				{"POST", prefix + "/user/drafts/" + page + "/discard/", false},
				{"POST", prefix + "/user/drafts/" + page + "/save", false},
				{"POST", prefix + "/user/drafts/" + page + "/restore", false},
				{"POST", prefix + "/user/drafts/open", false},
				{"GET", prefix + "/user/drafts/session", false},
				{"POST", prefix + "/user/drafts/check", false},
				{"POST", prefix + "/user/login", false},
				{"POST", prefix + "/alice/project/issues/new", false},
			} {
				before := calls.Load()
				req, err := http.NewRequest(tc.method, server.URL+tc.path, strings.NewReader("{}"))
				if err != nil {
					t.Fatal(err)
				}
				response, err := client.Do(req)
				if response != nil {
					response.Body.Close()
				}
				if tc.drop {
					if err == nil || response != nil || calls.Load() != before {
						t.Fatal("discard did not fail at transport before native dispatch")
					}
				} else {
					if err != nil {
						var transport *url.Error
						if errors.As(err, &transport) {
							t.Fatalf("unrelated route transport failed: %v", transport.Err)
						}
						t.Fatal("unrelated route request failed")
					}
					if response.StatusCode != http.StatusAccepted || response.Header.Get("X-Native-Fixture") != "unchanged" || calls.Load() != before+1 {
						t.Fatalf("unrelated native route changed: %s %s", tc.method, tc.path)
					}
				}
			}
		})
	}
}

func TestEditorDraftFixtureDropDiscardWithoutHijacker(t *testing.T) {
	nativeCalled := false
	base := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nativeCalled = true })
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "http://localhost/code/user/drafts/"+strings.Repeat("a", 64)+"/discard", nil)
	defer func() {
		if recover() != http.ErrAbortHandler || nativeCalled || w.Body.Len() != 0 || w.Flushed {
			t.Error("unsupported connection drop did not abort without native success")
		}
	}()
	draftFixtureHandler(base, draftFixtureOptions{prefix: "/code", failure: "drop-discard"}).ServeHTTP(w, r)
}

func TestEditorDraftFixtureDropDiscardOwnedExpiry(t *testing.T) {
	a := newViewApp(t)
	db := draftFixtureDB(t)
	service := user.NewDraftService()
	collection := strings.Repeat("c", 64)
	save := func() editordraft.Grant {
		t.Helper()
		grant, err := service.Open(context.Background(), a.alice.Id, collection, editordraft.Target{RepositoryID: a.repo.Id, Kind: "issue_new"})
		if err != nil {
			t.Fatal("native owned draft open failed")
		}
		before := time.Now()
		snapshot, err := service.Save(context.Background(), a.alice.Id, collection, grant.PageID, grant.PageSecret, 0, editordraft.Payload{Body: "owned unload fixture"})
		if err != nil || snapshot.Version != 1 || snapshot.ExpiresAt.Before(before.Add(30*time.Minute)) || snapshot.ExpiresAt.After(time.Now().Add(30*time.Minute)) {
			t.Fatal("native save did not retain the production 30-minute TTL")
		}
		persisted := readDraftFixtureRow(t, db, grant.PageID)
		if difference := snapshot.ExpiresAt.Sub(persisted.ExpiresAt); difference < 0 || difference >= time.Second {
			t.Fatal("persisted initial expiry differs from native 30-minute save expiry")
		}
		return grant
	}
	grant, other := save(), save()
	before, unrelated := readDraftFixtureRow(t, db, grant.PageID), readDraftFixtureRow(t, db, other.PageID)
	initial := make(chan struct{})
	var initialOnce sync.Once
	stopPurger := startDraftPurger(func(ctx context.Context) error {
		err := service.PurgeExpired(ctx)
		initialOnce.Do(func() { close(initial) })
		return err
	}, func() bool { return true })
	t.Cleanup(stopPurger)
	select {
	case <-initial:
	case <-time.After(3 * time.Second):
		t.Fatal("owned purger did not finish its initial pass")
	}
	observer := startDraftFixtureDiscardObserver(t, context.Background(), db)
	var nativeCalls atomic.Int32
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nativeCalls.Add(1)
		a.handler.ServeHTTP(w, r)
	})
	server := httptest.NewServer(draftFixtureHandler(base, draftFixtureOptions{prefix: "/gogs", failure: "drop-discard", discard: observer}))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = 3 * time.Second
	response, err := client.Get(server.URL + "/gogs/user/drafts/session")
	if err != nil || response.StatusCode != http.StatusUnauthorized || nativeCalls.Load() != 1 {
		t.Fatal("native unauthenticated control route did not remain available")
	}
	response.Body.Close()
	response, err = client.Post(server.URL+"/gogs/user/drafts/"+grant.PageID+"/discard", "application/json", strings.NewReader(`{"DisposalToken":"`+grant.DisposalToken+`"}`))
	if response != nil {
		response.Body.Close()
	}
	if err == nil || response != nil || nativeCalls.Load() != 1 {
		t.Fatal("owned discard reached native handler or returned a response")
	}
	aged := waitDraftFixtureEligibility(t, db, grant.PageID)
	before.ExpiresAt = aged.ExpiresAt
	if !reflect.DeepEqual(before, aged) || aged.Discarded || aged.Version != 1 || aged.PayloadJSON == "" {
		t.Fatal("failed discard did not retain the same saved row with only expiry aged")
	}
	if !reflect.DeepEqual(unrelated, readDraftFixtureRow(t, db, other.PageID)) {
		t.Fatal("aging changed another owned row")
	}
	select {
	case <-observer.done:
	case <-time.After(75 * time.Second):
		t.Fatal("existing one-minute purger did not remove the eligible owned row")
	}
	if found, err := db.Id(grant.PageID).Get(new(models.EditorDraft)); err != nil || found {
		t.Fatal("eligible owned row still exists after observed purge")
	}
	if !reflect.DeepEqual(unrelated, readDraftFixtureRow(t, db, other.PageID)) {
		t.Fatal("purger removed or changed the unrelated live row")
	}
}

func TestEditorDraftFixtureDropDiscardEarlyStop(t *testing.T) {
	newViewApp(t)
	db := draftFixtureDB(t)
	for _, afterDrop := range []bool{false, true} {
		t.Run(fmt.Sprint("after_drop=", afterDrop), func(t *testing.T) {
			page := strings.Repeat("b", 64)
			owner, cancel := context.WithCancel(context.Background())
			defer cancel()
			observer := startDraftFixtureDiscardObserver(t, owner, db)
			if afterDrop {
				row := &models.EditorDraft{PageID: page, Version: 1, PayloadJSON: `{}`, ExpiresAt: time.Now().Add(30 * time.Minute)}
				if _, err := db.Insert(row); err != nil {
					t.Fatal("owned stop fixture insert failed")
				}
				observer.dropped(page)
				waitDraftFixtureEligibility(t, db, page)
				cancel()
				select {
				case <-observer.done:
				case <-time.After(time.Second):
					t.Fatal("owner cancellation did not stop the purge observer")
				}
			}
			start := time.Now()
			observer.stop()
			if time.Since(start) > time.Second {
				t.Fatal("early stop waited for purger interval")
			}
			select {
			case <-observer.done:
			default:
				t.Fatal("stop returned before observer joined")
			}
			observer.dropped(page)
			if afterDrop {
				if found, err := db.Id(page).Get(new(models.EditorDraft)); err != nil || !found {
					t.Fatal("observer itself deleted the aged row on early stop")
				}
			}
		})
	}
}

func readDraftFixtureRow(t *testing.T, db *xorm.Engine, page string) models.EditorDraft {
	t.Helper()
	var row models.EditorDraft
	if found, err := db.Id(page).Get(&row); err != nil || !found {
		t.Fatal("expected owned fixture row is missing")
	}
	return row
}

func waitDraftFixtureEligibility(t *testing.T, db *xorm.Engine, page string) models.EditorDraft {
	t.Helper()
	deadline := time.After(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		row := readDraftFixtureRow(t, db, page)
		if !row.ExpiresAt.After(time.Now()) {
			eligible, err := db.Where("page_id = ? AND expires_at <= ?", page, time.Now().UTC()).Count(new(models.EditorDraft))
			if err != nil || eligible != 1 {
				t.Fatal("aged row is not eligible under the real purge SQL predicate")
			}
			return row
		}
		select {
		case <-deadline:
			t.Fatal("failed-discard row never became expiry eligible")
		case <-ticker.C:
		}
	}
}

func TestEditorDraftFixtureAttachments(t *testing.T) {
	enabled, path, types, files, size := setting.AttachmentEnabled, setting.AttachmentPath, setting.AttachmentAllowedTypes, setting.AttachmentMaxFiles, setting.AttachmentMaxSize
	t.Run("owned setup", func(t *testing.T) {
		root := t.TempDir()
		configureDraftFixtureAttachments(t, root)
		if !setting.AttachmentEnabled || setting.AttachmentPath != filepath.Join(root, "attachments") || setting.AttachmentAllowedTypes != "image/png" || setting.AttachmentMaxFiles != 10 || setting.AttachmentMaxSize != 32 {
			t.Fatal("fixture attachment settings not bounded/owned")
		}
		info, err := os.Stat(setting.AttachmentPath)
		if err != nil || !info.IsDir() {
			t.Fatalf("owned upload directory missing: %v", err)
		}
	})
	if setting.AttachmentEnabled != enabled || setting.AttachmentPath != path || setting.AttachmentAllowedTypes != types || setting.AttachmentMaxFiles != files || setting.AttachmentMaxSize != size {
		t.Fatal("fixture attachment settings leaked after cleanup")
	}
}

func TestEditorDraftFixtureOptions(t *testing.T) {
	defaults, err := parseDraftFixtureOptions("", "", "")
	if err != nil || defaults.idle != 45 || defaults.prefix != "/code" || defaults.failure != "" {
		t.Fatalf("changed default fixture: %+v %v", defaults, err)
	}
	for _, tc := range []struct{ idle, prefix, failure string }{{"10", "root", "save-outage"}, {"300", "/draft-cookies", "reject-cookie"}, {"45", "/draft-delay", "delay-save"}} {
		v, err := parseDraftFixtureOptions(tc.idle, tc.prefix, tc.failure)
		if err != nil {
			t.Fatal(err)
		}
		if tc.prefix == "root" && v.prefix != "" {
			t.Fatal("root prefix was not empty")
		}
	}
	for _, tc := range []struct{ idle, prefix, failure string }{{"9", "", ""}, {"301", "", ""}, {"bad", "", ""}, {"", "/code/../live", ""}, {"", "/draft-A", ""}, {"", "", "save-outage,reject-cookie"}} {
		if _, err := parseDraftFixtureOptions(tc.idle, tc.prefix, tc.failure); err == nil {
			t.Fatalf("unbounded fixture option accepted: %+v", tc)
		}
	}
	again, _ := parseDraftFixtureOptions("", "", "")
	if again != defaults {
		t.Fatal("options leaked between fixtures")
	}
}

func TestEditorDraftFixtureDelayScopeAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		status       int
		delayed      bool
	}{
		{"POST", "/draft-delay/user/drafts/page/save", 200, true},
		{"POST", "/draft-delay/user/drafts/page/save", 401, false},
		{"POST", "/draft-delay/user/drafts/open", 200, false},
		{"GET", "/draft-delay/user/drafts/session", 200, false},
		{"POST", "/draft-delay/user/login", 200, false},
		{"POST", "/draft-delay/user/drafts/page/discard", 200, false},
		{"GET", "/draft-delay/js/app/drafts.js", 200, false},
		{"POST", "/another/user/drafts/page/save", 200, false},
	} {
		w := httptest.NewRecorder()
		base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Fixture", "unchanged")
			w.WriteHeader(tc.status)
			w.Write([]byte("body"))
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		r := httptest.NewRequest(tc.method, "http://localhost"+tc.path, nil).WithContext(ctx)
		start := time.Now()
		draftFixtureHandler(base, draftFixtureOptions{idle: 10, prefix: "/draft-delay", failure: "delay-save"}).ServeHTTP(w, r)
		if time.Since(start) > time.Second {
			t.Fatal("cancelled request waited for delay")
		}
		if tc.delayed {
			if w.Body.Len() != 0 {
				t.Fatal("cancelled acknowledgement emitted body")
			}
		} else if w.Code != tc.status || w.Body.String() != "body" {
			t.Fatalf("unrelated/denial response delayed: %+v", tc)
		}
	}
	w := httptest.NewRecorder()
	w.Header().Set("X-Fixture", "unchanged")
	delayed := &draftFixtureResponse{ResponseWriter: w, ctx: context.Background(), delay: time.Millisecond}
	delayed.WriteHeader(200)
	delayed.Write([]byte("committed response"))
	if w.Code != 200 || w.Body.String() != "committed response" || w.Header().Get("X-Fixture") != "unchanged" {
		t.Fatal("delayed response changed")
	}
}

func TestEditorDraftFixtureCookieIsolation(t *testing.T) {
	base := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "native_session", Value: "opaque-auth", Path: "/", HttpOnly: true})
		http.SetCookie(w, &http.Cookie{Name: "_csrf", Value: "opaque-csrf", Path: "/"})
		http.SetCookie(w, &http.Cookie{Name: "hgs_draft", Value: "opaque-collection", Path: "/draft-cookies/user/drafts", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		w.Write([]byte("unchanged body"))
	})
	for _, failure := range []string{"", "reject-cookie"} {
		w := httptest.NewRecorder()
		draftFixtureHandler(base, draftFixtureOptions{failure: failure}).ServeHTTP(w, httptest.NewRequest("POST", "http://localhost/draft-cookies/user/drafts/open", nil))
		if w.Body.String() != "unchanged body" || w.Code != 200 {
			t.Fatal("response changed")
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 3 {
			t.Fatal("lost native cookies")
		}
		if cookies[0].Name != "native_session" || cookies[0].Value != "opaque-auth" || cookies[0].Path != "/" || !cookies[0].HttpOnly || cookies[1].Name != "_csrf" || cookies[1].Value != "opaque-csrf" || cookies[1].Path != "/" {
			t.Fatal("native authentication cookies changed")
		}
		for _, c := range cookies {
			if c.Name == "hgs_draft" && failure == "reject-cookie" {
				if c.Domain != "draft-cookie-rejected.invalid" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
					t.Fatalf("incorrect rejection cookie: %+v", c)
				}
			} else if c.Domain != "" {
				t.Fatalf("modified native/default cookie: %+v", c)
			}
		}
	}
}

func TestEditorDraftFixtureSaveOutage(t *testing.T) {
	newViewApp(t)
	db := draftFixtureDB(t)
	if err := installDraftFixtureOutage(db); err != nil {
		t.Fatal(err)
	}
	row := &models.EditorDraft{PageID: "owned-fixture-row", PayloadJSON: `{"Body":"initial"}`}
	if _, err := db.Insert(row); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Id(row.PageID).Cols("payload_json").Update(&models.EditorDraft{PayloadJSON: `{"Body":"save"}`}); err == nil || !strings.Contains(err.Error(), "fixture draft save unavailable") {
		t.Fatalf("save was not rejected: %v", err)
	}
	if _, err := db.Id(row.PageID).Cols("payload_json", "discarded").Update(&models.EditorDraft{Discarded: true}); err != nil {
		t.Fatal("discard blocked", err)
	}
	if _, err := db.Id(row.PageID).Delete(new(models.EditorDraft)); err != nil {
		t.Fatal("purge blocked", err)
	}
}
