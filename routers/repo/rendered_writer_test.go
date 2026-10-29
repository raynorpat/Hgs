package repo

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 3 && os.Args[1] == "-R" {
		if os.Args[3] == "cat" {
			chunk := []byte(strings.Repeat("x", 32768))
			for n := 0; n < 128; n++ {
				if _, err := os.Stdout.Write(chunk); err != nil {
					os.Exit(1)
				}
			}
		} else if os.Args[3] == "log" {
			os.Stdout.WriteString(strings.Repeat("a", 40))
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestRenderedWriterActualRunnerBound(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	runner, err := hg.NewRunner(hg.Config{Executable: executable, ConfigPath: filepath.Join(root, "hgrc"), MaxProcesses: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for n := 0; n < 2; n++ {
		w := new(renderedWriter)
		err := runner.ReadFile(ctx, root, hg.NodeID(strings.Repeat("a", 40)), "README.md", w)
		if w.Len() != hg.MaxRenderedFileBytes+1 || !errors.Is(err, errRenderedLimit) {
			t.Fatalf("retained=%d want=%d error=%v", w.Len(), hg.MaxRenderedFileBytes+1, err)
		}
	}
	// The second read needs the sole child slot; pattern files cannot outlive reads.
	entries, err := filepath.Glob(filepath.Join(root, "cat-*"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("read lifetime leaked: %v %v", entries, err)
	}
}
