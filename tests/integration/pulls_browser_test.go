//go:build integration

package integration

import (
	"context"
	"fmt"
	"github.com/gogits/gogs/models"
	"github.com/gogits/gogs/modules/middleware"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPullBrowserFixture(t *testing.T) {
	if os.Getenv("UI_PULL_FIXTURE") != "1" {
		t.Skip("opt-in disposable PR browser fixture")
	}
	f := seededPullHTTP(t)
	p := f.create(t)
	c, e := f.service.RefreshComparison(context.Background(), f.owner.Id, p.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = models.AddAccess(&models.Access{UserId: f.reader.Id, RepoId: f.public.Id, Mode: models.WRITABLE}); e != nil {
		t.Fatal(e)
	}
	expectPullPost(t, f.uiFixture, f.route+"/1/reviewers", "owner", url.Values{"reviewers": {f.reader.Name}}, 303)
	expectPullPost(t, f.uiFixture, f.route+"/1/comments", "reader", url.Values{"content": {"An inline comment on this immutable comparison."}, "comparison_id": {fmt.Sprint(c.ID)}, "path": {"proposal.txt"}, "side": {"source"}, "line": {"1"}}, 303)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = middleware.WithBrowserResponseController(r, w)
		f.m.ServeHTTP(w, r)
		if middleware.BrowserResponseAborted(r) {
			panic(http.ErrAbortHandler)
		}
	})}
	stop := make(chan struct{})
	var once sync.Once
	f.m.Get("/fixture/stop", func(ctx *middleware.Context) {
		ctx.Write([]byte("stopping disposable fixture"))
		once.Do(func() { close(stop) })
	})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Logf("PULL_BROWSER_URL=http://%s%s%s/1", listener.Addr(), f.prefix, f.route)
	t.Logf("roles /code/fixture/account/owner and /code/fixture/account/reader; anonymous /code/fixture/account/anonymous; source advance/revoke through owned native fixture setup only; shutdown /code/fixture/stop; owned root %s", f.root)
	select {
	case <-stop:
	case <-time.After(45 * time.Minute):
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = server.Shutdown(ctx); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != http.ErrServerClosed {
		t.Fatal(e)
	}
}
