package cmd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
)

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
