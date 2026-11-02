package repo

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hg"
)

func TestRenderedWriterActualRunnerBound(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner, err := hg.NewRunner(hg.Config{Executable: executable, ConfigPath: filepath.Join(root, "hgrc"), MaxProcesses: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The fake read service streams far more than the rendered limit.
	service := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := []byte(strings.Repeat("x", 32768))
		for n := 0; n < 128; n++ {
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}))
	t.Cleanup(service.Close)
	reads := hg.NewReadClient(root)
	reads.SetEndpoint(strings.TrimPrefix(service.URL, "http://"), "")
	runner.SetReads(reads)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for n := 0; n < 2; n++ {
		w := new(renderedWriter)
		err := runner.ReadFile(ctx, filepath.Join(root, "repo"), hg.NodeID(strings.Repeat("a", 40)), "README.md", w)
		if w.Len() != hg.MaxRenderedFileBytes+1 || !errors.Is(err, errRenderedLimit) {
			t.Fatalf("retained=%d want=%d error=%v", w.Len(), hg.MaxRenderedFileBytes+1, err)
		}
	}
}
