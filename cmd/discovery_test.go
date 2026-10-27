package cmd

import (
	"github.com/gogits/gogs/modules/setting"
	"testing"
)

func TestCatalogRegisteredRoutesPreserveInstallGuard(t *testing.T) {
	a := newViewApp(t)
	setting.InstallLock = false
	for _, path := range []string{"/gogs/explore", "/gogs/explore/search?q=project"} {
		w := a.request(t, "GET", path, "", "")
		if w.Code != 302 || w.Header().Get("Location") != "/gogs/install" {
			t.Fatalf("%s missing install boundary: %d %s", path, w.Code, w.Header().Get("Location"))
		}
	}
}
