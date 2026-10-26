// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func values(m map[string]string) []string {
	var v []string
	for _, s := range m {
		v = append(v, s)
	}
	return v
}

func TestReaderHooksDenyWithoutController(t *testing.T) {
	exe, _ := os.Executable()
	for _, canWrite := range []bool{false, true} {
		cfg, err := HookConfig(exe, canWrite)
		if err != nil {
			t.Fatal(err)
		}
		var keys []string
		for k := range cfg {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := "hooks.prepushkey hooks.pretxnclose hooks.pretxnclose-bookmark hooks.pretxnclose-phase hooks.pretxnopen"
		if got := strings.Join(keys, " "); got != want {
			t.Fatalf("hook keys %q, want %q", got, want)
		}
		mode := "deny"
		if canWrite {
			mode = "check"
		}
		for k, v := range cfg {
			if !strings.HasSuffix(v, " internal-hg-hook "+mode+" "+strings.TrimPrefix(k, "hooks.")) {
				t.Errorf("%s = %q", k, v)
			}
		}
	}

	exe = filepath.Join(t.TempDir(), "bin", "gogs.exe")
	if cfg, _ := HookConfig(exe, false); strings.Contains(strings.Join(values(cfg), " "), "check") {
		t.Fatal("read-only hook configuration can reach the capability callback")
	}
}

// windowsAccepted are executable paths quoteFor accepts for cmd.exe; the
// integration test proves Mercurial's tonative rewrite leaves their hook
// commands intact.
var windowsAccepted = []string{
	`C:\Program Files\Gogs é ü\PROGRA~1\gogs.exe`,
	`C:\a&b(c)^d;e,f=g[h]{i}#j@k+l\gogs.exe`,
}

func TestHookExecutablePathQuoting(t *testing.T) {
	for _, tc := range []struct {
		goos, exe, want string
	}{
		{"windows", windowsAccepted[0], `"` + windowsAccepted[0] + `"`},
		{"windows", windowsAccepted[1], `"` + windowsAccepted[1] + `"`},
		{"linux", `/opt/Gogs é ü/gogs`, `'/opt/Gogs é ü/gogs'`},
		{"linux", `/opt/it's "$HOME" ` + "`x`" + `/gogs`, `'/opt/it'\''s "$HOME" ` + "`x`" + `/gogs'`},
		{"darwin", `/Applications/Gogs %PATH%!/gogs`, `'/Applications/Gogs %PATH%!/gogs'`},
	} {
		got, err := quoteFor(tc.goos, tc.exe)
		if err != nil || got != tc.want {
			t.Errorf("quoteFor(%s, %q) = %q, %v; want %q", tc.goos, tc.exe, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ goos, exe string }{
		{"windows", `C:\a"b\gogs.exe`},
		{"windows", `C:\100%PATH%\gogs.exe`},
		{"windows", `C:\bang!\gogs.exe`},
		{"windows", `C:\x'&echo PWN&'\gogs.exe`},
		{"windows", `C:\it's\gogs.exe`},
		{"windows", `C:\$HG\gogs.exe`},
		{"windows", `C:\${HG}\gogs.exe`},
		{"windows", `C:\a~\gogs.exe`},
		{"windows", `C:\a~/gogs.exe`},
		{"windows", `C:\~a\gogs.exe`},
		{"windows", "C:\\new\nline\\gogs.exe"},
		{"linux", "/new\nline/gogs"},
	} {
		if got, err := quoteFor(tc.goos, tc.exe); err == nil {
			t.Errorf("quoteFor(%s, %q) = %q, want an error", tc.goos, tc.exe, got)
		}
	}
	if _, err := HookConfig("gogs", true); err == nil {
		t.Fatal("relative executable accepted")
	}
	if runtime.GOOS == "windows" {
		if _, err := HookConfig(filepath.Join(t.TempDir(), "x'&echo PWN&'", "gogs.exe"), true); err == nil {
			t.Fatal("HookConfig accepted a path Mercurial's tonative rewrite would alter")
		}
	}
}
