package cmd

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gogits/gogs/modules/hgbridge"
	"github.com/gogits/gogs/modules/setting"
)

func TestHostingDispatchBeforeBrowserMiddleware(t *testing.T) {
	holder := &hostingHolder{}
	browserCalls := 0
	handler := holder.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { browserCalls++; w.WriteHeader(418) }))
	for _, path := range []string{"/hg/alice/repo?cmd=capabilities", "/hg/alice/re%2fpo?cmd=unbundle"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("POST", "http://host"+path, nil))
		if w.Code != 503 {
			t.Fatalf("uninstalled transport: %d", w.Code)
		}
	}
	if browserCalls != 0 {
		t.Fatal("transport traversed browser middleware")
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "http://host/user/login", nil))
	if browserCalls != 1 || w.Code != 418 {
		t.Fatal("browser path not retained")
	}
}

func TestHostingActualMacaronRegistration(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	oldRoot, oldConf, oldCustom, oldAdapter, oldSession := setting.StaticRootPath, setting.ConfRootPath, setting.CustomPath, setting.CacheAdapter, setting.SessionConfig
	defer func() {
		setting.StaticRootPath, setting.ConfRootPath, setting.CustomPath, setting.CacheAdapter, setting.SessionConfig = oldRoot, oldConf, oldCustom, oldAdapter, oldSession
	}()
	oldLangs, oldNames := setting.Langs, setting.Names
	defer func() { setting.Langs, setting.Names = oldLangs, oldNames }()
	setting.Langs = []string{"en-US"}
	setting.Names = []string{"English"}
	setting.StaticRootPath = root
	setting.ConfRootPath = filepath.Join(root, "conf")
	setting.CustomPath = t.TempDir()
	setting.CacheAdapter = "memory"
	setting.SessionConfig.Provider = "memory"
	handler := newWebHandler(&hostingHolder{})
	for _, path := range []string{"/hg/alice/project?cmd=unbundle", "/hg/alice/pro%2fject?cmd=unbundle"} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "http://host"+path, nil)
		r.Header.Set("Cookie", "i_like_gogits=alice")
		handler.ServeHTTP(w, r)
		if w.Code != 503 || w.Header().Get("Set-Cookie") != "" {
			t.Fatalf("transport traversed browser middleware: %d %v", w.Code, w.Header())
		}
	}
}

// The integration worker runs the same hook implementation through the test executable.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == hgbridge.HookCommand {
		if err := hgHook(context.Background(), os.Args[2:], os.Environ()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-test.") {
		os.Exit(fakeViewHg(os.Args[1:]))
	}
	os.Exit(m.Run())
}

func TestHostingDispatchWithHGSubURL(t *testing.T) {
	old := setting.AppSubUrl
	setting.AppSubUrl = "/hg"
	defer func() { setting.AppSubUrl = old }()
	browserCalls := 0
	handler := (&hostingHolder{}).Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { browserCalls++ }))
	for _, path := range []string{"/hg/alice/project", "/hg/hg/alice/project"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "http://host"+path+"?cmd=capabilities", nil))
		if w.Code != 503 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if browserCalls != 0 {
		t.Fatal("hg app sub-URL entered browser middleware")
	}
}

func TestHostingComparisonRootFailureBeforePublication(t *testing.T) {
	a := newViewApp(t)
	if err := a.holder.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	// A new initialization must not expose a transport when comparison storage
	// overlaps published repositories. Existing disposable stores stay intact.
	setting.Mercurial.ReviewArtifactRoot = filepath.Join(setting.RepoRootPath, "comparisons")
	holder := &hostingHolder{errors: make(chan error, 1)}
	if err := holder.Initialize(); err == nil {
		t.Fatal("unsafe comparison root accepted")
	}
	if holder.Service() != nil || holder.sshListener != nil {
		t.Fatal("service/listener published before recovery")
	}
	if holder.contributionDone != nil || holder.contributionCancel != nil {
		t.Fatal("contribution worker started before initialization/recovery succeeded")
	}
	if err := holder.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(setting.RepoRootPath); err != nil {
		t.Fatal(err)
	}
}

func TestProfileContributionHolderLifecycle(t *testing.T) {
	a := newViewApp(t)
	w := a.request(t, "GET", "/gogs/alice?tab=activity", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "data-contribution-calendar") {
		t.Fatalf("initialized profile HTTP %d", w.Code)
	}
	if a.holder.Contributions() == nil {
		t.Fatal("initialized holder did not expose existing index")
	}
	if err := a.holder.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.holder.Contributions() != nil {
		t.Fatal("closed holder exposed index")
	}
	w = a.request(t, "GET", "/gogs/alice?tab=activity", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-contribution-state="unavailable"`) {
		t.Fatalf("closed service must render honest unavailable profile HTTP %d", w.Code)
	}
	if (&hostingHolder{}).Contributions() != nil {
		t.Fatal("uninitialized holder returned typed nil reader")
	}
}
