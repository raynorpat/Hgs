// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package hgbridge

import (
	"os"
	"path/filepath"
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

func TestHookExecutablePathQuoting(t *testing.T) {
	for _, tc := range []struct {
		goos, exe, want string
	}{
		{"windows", `C:\Program Files\Gogs é ü\it's\gogs.exe`, `"C:\Program Files\Gogs é ü\it's\gogs.exe"`},
		{"windows", `C:\a&b(c)^d\gogs.exe`, `"C:\a&b(c)^d\gogs.exe"`},
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
}
