package cmd

import (
	"context"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/setting"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// All failures are fixed before listen against owned fixture resources. No
// request can control failure state or create an authentication session.
func TestStatusBrowserFixture(t *testing.T) {
	if os.Getenv("UI_STATUS_FIXTURE") != "1" {
		t.Skip("owned opt-in status browser fixture")
	}
	executable := os.Getenv("GOGS_TEST_HG")
	if executable == "" {
		t.Fatal("GOGS_TEST_HG required")
	}
	mode := os.Getenv("UI_STATUS_MODE")
	if mode != "" && mode != "db-closed" && mode != "missing-image" {
		t.Fatal("invalid status fixture mode")
	}
	a := newViewAppPrefix(t, executable, "/code")
	setting.Langs, setting.Names = []string{"en-US", "de-DE"}, []string{"English", "Deutsch"}
	if mode == "missing-image" {
		assets := filepath.Join(t.TempDir(), "assets")
		for _, name := range []string{"public", "templates", "conf"} {
			if err := os.CopyFS(filepath.Join(assets, name), os.DirFS(filepath.Join(setting.StaticRootPath, name))); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(filepath.Join(assets, "public/img/hgs-error.png")); err != nil {
			t.Fatal(err)
		}
		setting.StaticRootPath = assets
	}
	a.handler = newWebHandler(a.holder)
	if mode != "" {
		if err := a.holder.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := models.CloseEngine(); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: a.handler}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		server.Shutdown(ctx)
		<-done
	}()
	t.Logf("STATUS_BROWSER_URL=http://%s/code/ MODE=%s 404=/code/no/such/page 500=/code/explore STOP_FILE=%s lifetime=8m", listener.Addr(), mode, filepath.Join(a.root, "stop"))
	deadline := time.Now().Add(8 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(filepath.Join(a.root, "stop")); err == nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}
