package hosting

import (
	"context"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/hgbridge"
)

func TestHostingDrainBlocksAdmissionsAndWaits(t *testing.T) {
	r := newRegistry()
	canceled := make(chan struct{})
	release, err := r.Register(hgbridge.Grant{RepoID: 7}, func() { close(canceled) })
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- r.drain(context.Background(), 7) }()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("worker not canceled")
	}
	if _, err := r.Register(hgbridge.Grant{RepoID: 7}, func() {}); err == nil {
		t.Fatal("admitted deleting repository")
	}
	select {
	case <-done:
		t.Fatal("drain returned before worker release")
	default:
	}
	release()
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestHostingDrainAdmissionRace(t *testing.T) {
	for i := 0; i < 100; i++ {
		r := newRegistry()
		joined := make(chan struct{})
		go func() {
			defer close(joined)
			release, err := r.Register(hgbridge.Grant{RepoID: 9}, func() {})
			if err == nil {
				release()
			}
		}()
		if err := r.drain(context.Background(), 9); err != nil {
			t.Fatal(err)
		}
		<-joined
		if _, err := r.Register(hgbridge.Grant{RepoID: 9}, func() {}); err == nil {
			t.Fatal("late admission")
		}
	}
}
