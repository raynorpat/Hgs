package hosting

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
	"golang.org/x/sys/windows"
)

// Diagnose the native fixture's intermittent staging rename without retries in
// production. Each child records its actual cwd; Wait must precede the move.
func TestHostingWindowsRenameAfterChildExit(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	r, err := hg.NewRunner(hg.Config{Executable: exe, ConfigPath: filepath.Join(root, "config", "hgrc")})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		stage := filepath.Join(root, fmt.Sprint(i)+"-stage")
		trace := filepath.Join(root, "trace")
		c, err := r.Start(context.Background(), "", []string{"init", stage}, map[string]string{"GOGS_HG_TEST_TRACE": trace})
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, c.Stdout)
		io.Copy(io.Discard, c.Stderr)
		if err = c.Wait(); err != nil {
			t.Fatal(err)
		}
		c.Stdout.Close()
		c.Stderr.Close()
		b, err := os.ReadFile(trace)
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.SplitN(string(b), "\n", 2)
		pid, _ := strconv.Atoi(fields[0])
		live := false
		if handle, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)); e == nil {
			var code uint32
			if e = windows.GetExitCodeProcess(handle, &code); e != nil {
				t.Fatal(e)
			}
			live = code == 259
			windows.CloseHandle(handle)
		}
		if live {
			t.Fatalf("Wait returned with live child %d cwd=%q", pid, fields[1])
		}
		if err = os.Rename(stage, filepath.Join(root, fmt.Sprint(i))); err != nil {
			// Diagnostic only: report whether the lock survives process exit and
			// a short observation interval. The initial failure still fails.
			time.Sleep(100 * time.Millisecond)
			again := os.Rename(stage, filepath.Join(root, fmt.Sprint(i)))
			t.Fatalf("iteration=%d pid=%d exited=%t cwd=%q rename=%v; after 100ms=%v", i, pid, !live, fields[1], err, again)
		}
	}
}
