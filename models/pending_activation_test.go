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

func TestPendingActivationProfileAndIdentityChanges(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*User)
		pending bool
	}{
		{"profile", func(u *User) { u.FullName = "Pending Member" }, true},
		{"primary-email", func(u *User) { u.Email = "changed@example.invalid" }, false},
		{"login-type", func(u *User) { u.LoginType = PLAIN }, false},
		{"login-name", func(u *User) { u.LoginName = "changed-login" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			newTestDatabase(t)
			newAccessTestUser(t, "administrator", true)
			u := &User{Name: "pending-identity", Email: "pending-identity@example.invalid", Passwd: "password", PendingActivation: true}
			if err := CreateUser(u); err != nil {
				t.Fatal(err)
			}
			tc.change(u)
			if err := UpdateUser(u); err != nil {
				t.Fatal(err)
			}
			current, err := GetUserById(u.Id)
			if err != nil {
				t.Fatal(err)
			}
			if current.PendingActivation != tc.pending {
				t.Fatalf("pending=%v want=%v", current.PendingActivation, tc.pending)
			}
			_, err = ReservePendingActivation(context.Background(), u.Name, "password", time.Now())
			if tc.pending && err != nil {
				t.Fatal(err)
			}
			if !tc.pending && !errors.Is(err, ErrEmailProofInvalid) {
				t.Fatalf("changed identity admitted proof: %v", err)
			}
		})
	}
}
