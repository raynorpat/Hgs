package hgbridge

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestContributionPublicationIsAdvisory(t *testing.T) {
	checks := 0
	c, err := NewController(func(context.Context, Grant) error { checks++; return ErrDenied }, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	notified := int64(0)
	c.SetPublicationObserver(func(ctx context.Context, id int64) error { notified = id; return errors.New("sink unavailable") })
	cap, _, revoke, err := c.Issue(Grant{Principal: Principal{UserID: 1}, RepoID: 42, CanWrite: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Published(context.Background(), cap, 42, "txnclose", ""); err == nil {
		t.Fatal("observer failure not observable")
	}
	if checks != 0 || notified != 42 {
		t.Fatalf("notification reauthorized or misidentified: checks=%d repo=%d", checks, notified)
	}
	if err = c.Check(context.Background(), cap, 42, "pretxnopen"); !errors.Is(err, ErrDenied) || checks != 1 {
		t.Fatalf("writer gate changed: %v checks=%d", err, checks)
	}
	notified = 0
	if err = c.Published(context.Background(), cap, 43, "txnclose", ""); err == nil || notified != 0 {
		t.Fatal("cross-repository publication accepted")
	}
	revoke()
	if err = c.Published(context.Background(), cap, 42, "txnclose", ""); err == nil || notified != 0 {
		t.Fatal("revoked capability accepted")
	}
}
