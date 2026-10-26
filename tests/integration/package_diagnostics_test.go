//go:build integration

package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPortablePackageDiagnostics(t *testing.T) {
	if os.Getenv("GOGS_TEST_PACKAGE_DIRECT") != "1" {
		t.Skip("requires freshly extracted package")
	}
	a := newApplication(t, matrix(t)[0])
	a.stop()
	if !a.lastCleanStop {
		t.Fatal("diagnostics require clean stopped fixture")
	}
	source := filepath.Join(a.root, "incompatible-hg.go")
	writePackageFile(t, source, []byte("package main\nimport \"fmt\"\nfunc main(){fmt.Println(`[ {\"ver\": \"4.9\"} ]`)}\n"))
	fake := filepath.Join(a.root, "incompatible-hg")
	if runtime.GOOS == "windows" {
		fake += ".exe"
	}
	build := exec.Command("go", "build", "-o", fake, source)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build controlled version diagnostic fixture: %v %s", err, out)
	}
	original := string(readPackageFile(t, a.config))
	for _, tc := range []struct{ name, exe, want string }{
		{"missing", filepath.Join(a.root, "missing-hg"), "is missing:"},
		{"incompatible controlled 4.9 output", fake, "version 4.9 is too old"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := strings.Replace(original, "EXECUTABLE = "+filepath.ToSlash(hgPath(t)), "EXECUTABLE = "+filepath.ToSlash(tc.exe), 1)
			name := filepath.Join(a.root, "diagnostic.ini")
			writePackageFile(t, name, []byte(config))
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			c := exec.CommandContext(ctx, a.binary, "web", "--config", name)
			c.Dir = a.root
			c.Env = replaceEnv(os.Environ(), "PATH", os.Getenv("GOGS_TEST_SERVER_PATH"))
			c.Env = replaceEnv(c.Env, "GOGS_CUSTOM", filepath.Join(a.root, "custom"))
			out, err := c.CombinedOutput()
			if ctx.Err() != nil || err == nil || !strings.Contains(string(out), tc.want) {
				t.Fatalf("diagnostic want=%q exit=%v timeout=%v output=%s", tc.want, err, ctx.Err(), out)
			}
			for _, line := range strings.Split(string(out), "\n") {
				if strings.Contains(line, "Web server failed") {
					t.Log(strings.TrimSpace(line))
				}
			}
		})
	}
	if left := waitNoProcesses(a.binary, a.repoRoot); len(left) != 0 {
		t.Fatalf("diagnostic startup left processes: %v", left)
	}
}
