package models

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPendingActivationSecurityBoundary(t *testing.T) {
	newTestDatabase(t)
	newAccessTestUser(t, "administrator", true)
	u := &User{Name: "pending", Email: "pending@example.invalid", Passwd: "initial-password", PendingActivation: true}
	if err := CreateUser(u); err != nil {
		t.Fatal(err)
	}
	stale := *u
	ctx := context.Background()
	now := time.Now()
	if _, err := ReservePendingActivation(ctx, u.Name, "initial-password", now); err != nil {
		t.Fatal(err)
	}
	if _, err := ReservePendingActivation(ctx, u.Name, "initial-password", now.Add(time.Second)); !errors.Is(err, ErrEmailProofLimited) {
		t.Fatal(err)
	}
	// Admin mutation commits under the same email/account lock before the
	// delayed credential proof reaches admission.
	disabled := make(chan error, 1)
	go func() { u.PendingActivation = false; disabled <- UpdateUser(u) }()
	if err := <-disabled; err != nil {
		t.Fatal(err)
	}
	if _, err := ReservePendingActivation(ctx, u.Name, "initial-password", now.Add(181*time.Second)); !errors.Is(err, ErrEmailProofInvalid) {
		t.Fatalf("stale proof admitted: %v", err)
	}
	if err := UpdateUser(&stale); err != nil {
		t.Fatal(err)
	}
	current, err := GetUserById(u.Id)
	if err != nil || current.PendingActivation {
		t.Fatalf("stale object revived pending proof %+v %v", current, err)
	}
	if _, err := UserSignIn(u.Name, "initial-password"); err == nil {
		t.Fatal("inactive password authenticated")
	}
}
