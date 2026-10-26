package ssh

import (
	"context"
	"github.com/gogits/gogs/modules/hgbridge"
	"testing"
	"time"
)

type lifecycleRegistrar func(hgbridge.Grant, context.CancelFunc) (func(), error)

func (f lifecycleRegistrar) Register(g hgbridge.Grant, c context.CancelFunc) (func(), error) {
	return f(g, c)
}
func TestSSHLifecycleWaitingCredentialAndBlockedWorker(t *testing.T) {
	for _, waiting := range []bool{true, false} {
		ts := newTestServer(t, 0)
		registered := make(chan context.CancelFunc, 2)
		released := make(chan struct{}, 2)
		ts.s.Registrar = lifecycleRegistrar(func(g hgbridge.Grant, c context.CancelFunc) (func(), error) {
			if g.RepoID != 0 || g.Principal.UserID != 1 {
				t.Errorf("waiting credential: %+v", g)
			}
			registered <- c
			return func() { released <- struct{}{} }, nil
		})
		ts.s.bridge.Registrar = lifecycleRegistrar(func(g hgbridge.Grant, c context.CancelFunc) (func(), error) {
			if g.RepoID != 9 || !g.CanWrite {
				t.Errorf("worker: %+v", g)
			}
			registered <- c
			return func() { released <- struct{}{} }, nil
		})
		client, err := ts.dial("hg", ts.writer)
		if err != nil {
			t.Fatal(err)
		}
		defer client.Close()
		cancel := <-registered
		if !waiting {
			client.Close()
			<-released
			client = ts.blocked()
			defer client.Close()
			<-registered
			cancel = <-registered
		}
		cancel()
		select {
		case <-released:
		case <-time.After(3 * time.Second):
			t.Fatal("targeted cancel did not release SSH session")
		}
		waitFor(t, "targeted SSH cleanup", func() bool { return ts.liveConns() == 0 })
	}
}
