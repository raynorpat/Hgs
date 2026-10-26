package models

import (
	"errors"
	"testing"
)

func TestRepositoryPasswordIdentityVersionRevokesOnChange(t *testing.T) {
	newTestDatabase(t)
	user := newAccessTestUser(t, "alice", true)
	identity, err := AuthenticateRepositoryHTTPSIdentity("alice", "alice-password")
	if err != nil {
		t.Fatal(err)
	}
	if identity.PasswordVersion == "" || identity.TokenID != 0 || identity.UserID != user.Id {
		t.Fatalf("missing password identity: %+v", identity)
	}
	if err = ValidateRepositoryPasswordVersion(user.Id, identity.PasswordVersion); err != nil {
		t.Fatal(err)
	}
	authenticated, err := UserSignIn("alice", "alice-password")
	if err != nil {
		t.Fatal(err)
	}
	user.Passwd = "new-password"
	user.Salt = GetUserSalt()
	user.EncodePasswd()
	if err = UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	// Version capture is bound to the very row which passed password validation,
	// even if its database row changes before the auth result is assembled.
	captured, err := repositoryPasswordIdentity(authenticated)
	if err != nil {
		t.Fatal(err)
	}
	if captured.PasswordVersion != identity.PasswordVersion {
		t.Fatal("captured a later password reset version")
	}
	if err = ValidateRepositoryPasswordVersion(user.Id, identity.PasswordVersion); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("stale password version valid: %v", err)
	}
	if err = ValidateRepositoryPasswordVersion(user.Id, ""); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("missing password version accepted: %v", err)
	}
	fresh, err := AuthenticateRepositoryHTTPSIdentity("alice", "new-password")
	if err != nil {
		t.Fatal(err)
	}
	if fresh.PasswordVersion == identity.PasswordVersion {
		t.Fatal("password reset did not change version")
	}
	if err = ValidateRepositoryPasswordVersion(user.Id, fresh.PasswordVersion); err != nil {
		t.Fatal(err)
	}
	user.LoginName = "changed-provider-login"
	if err = UpdateUser(user); err != nil {
		t.Fatal(err)
	}
	if err = ValidateRepositoryPasswordVersion(user.Id, fresh.PasswordVersion); !errors.Is(err, ErrInvalidCredential) {
		t.Fatalf("changed login identity retained password version: %v", err)
	}
}
