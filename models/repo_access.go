// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/gogits/gogs/modules/setting"
)

// ErrInvalidCredential is returned for any rejected repository credential:
// unknown account, wrong password, revoked key or token, or disabled account.
var ErrInvalidCredential = errors.New("Invalid repository credential")

// RepositoryAccess is what a user may do with a repository. Admin implies
// Write, and Write implies Read.
type RepositoryAccess struct {
	Read, Write, Admin bool
}

// RepositoryAccessFor returns the current access of user to repository,
// shared by the web UI and both transports. userID 0 means anonymous.
// It reads the database on every call; any error means no access.
func RepositoryAccessFor(userID, repoID int64) (RepositoryAccess, error) {
	return repositoryAccessFor(x, userID, repoID, false)
}
func repositoryAccessFor(e Engine, userID, repoID int64, locked bool) (RepositoryAccess, error) {
	ancestors, err := forkAncestors(e, repoID, locked)
	if err != nil {
		return RepositoryAccess{}, err
	}
	// Apply ancestry before every direct-access return, including anonymous and
	// owner/admin implication. Missing or non-ready ancestors fail closed.
	for _, id := range ancestors {
		var parent Repository
		has, err := policyGet(e, locked, "repository", "id = ? AND state = ?", &parent, id, RepositoryReady)
		if err != nil {
			return RepositoryAccess{}, err
		}
		if !has {
			return RepositoryAccess{}, ErrForkLineage
		}
		if parent.IsPrivate {
			a, err := directRepositoryAccess(e, userID, id, locked)
			if err != nil || !a.Read {
				return RepositoryAccess{}, err
			}
		}
	}
	return directRepositoryAccess(e, userID, repoID, locked)
}
func directRepositoryAccess(e Engine, userID, repoID int64, locked bool) (RepositoryAccess, error) {
	var access RepositoryAccess
	repo := new(Repository)
	has, err := policyGet(e, locked, "repository", "id = ? AND state = ?", repo, repoID, RepositoryReady)
	if err != nil {
		return access, err
	}
	if !has {
		return access, ErrRepoNotExist
	}
	if userID == 0 {
		access.Read = !repo.IsPrivate && !setting.Service.RequireSignInView
		return access, nil
	}
	u, err := enabledPolicyAccount(e, userID, locked)
	if err == ErrInvalidCredential {
		return access, nil
	}
	if err != nil {
		return access, err
	}
	row := new(Access)
	hasRow, err := policyGet(e, locked, "access", "user_id = ? AND repo_id = ?", row, u.Id, repo.Id)
	if err != nil {
		return access, err
	}
	access.Admin = repo.OwnerId == u.Id
	access.Write = hasRow && row.Mode >= WRITABLE
	access.Read = !repo.IsPrivate || hasRow
	owner := new(User)
	has, err = policyGet(e, locked, "user", "id = ?", owner, repo.OwnerId)
	if err != nil {
		return RepositoryAccess{}, err
	}
	if !has {
		return RepositoryAccess{}, ErrUserNotExist
	}
	if owner.IsOrganization() {
		access.Admin, err = policyGet(e, locked, "org_user", "uid = ? AND org_id = ? AND is_owner = ?", new(OrgUser), u.Id, owner.Id, true)
		if err != nil {
			return RepositoryAccess{}, err
		}
	}
	access.Write = access.Write || access.Admin
	access.Read = access.Read || access.Write
	return access, nil
}
func enabledPolicyAccount(e Engine, userID int64, locked bool) (*User, error) {
	if userID <= 0 {
		return nil, ErrInvalidCredential
	}
	u := new(User)
	has, err := policyGet(e, locked, "user", "id = ?", u, userID)
	if err != nil {
		return nil, err
	}
	if !has || !u.IsActive || u.IsOrganization() {
		return nil, ErrInvalidCredential
	}
	return u, nil
}

// enabledAccount returns the individual account of given ID, or
// ErrInvalidCredential if it does not exist or may not sign in.
func enabledAccount(userID int64) (*User, error) {
	if userID <= 0 {
		return nil, ErrInvalidCredential
	}
	u := new(User)
	has, err := x.Id(userID).Get(u)
	if err != nil {
		return nil, err
	} else if !has || !u.IsActive || u.IsOrganization() {
		return nil, ErrInvalidCredential
	}
	return u, nil
}

// RepositoryHTTPSIdentity carries the credential which actually authenticated.
// PasswordVersion is an opaque, non-secret local credential revision; it is
// computed from the same user row whose password/provider check succeeded.
type RepositoryHTTPSIdentity struct {
	UserID, TokenID int64
	PasswordVersion string
}

func repositoryPasswordVersion(u *User) string {
	loginType := u.LoginType
	if loginType == NOTYPE {
		loginType = PLAIN
	}
	h := sha256.New()
	fmt.Fprintf(h, "gogs-repository-password-v1\x00%s\x00%s\x00%d\x00%d\x00%s\x00%s\x00%s", u.Passwd, u.Salt, loginType, u.LoginSource, u.LoginName, u.LowerName, u.Email)
	return hex.EncodeToString(h.Sum(nil))
}
func repositoryPasswordIdentity(u *User) (RepositoryHTTPSIdentity, error) {
	if u.Id <= 0 || !u.IsActive || u.IsOrganization() {
		return RepositoryHTTPSIdentity{}, ErrInvalidCredential
	}
	// Do not fetch a newer row here: a reset racing this result must never
	// turn a successful old-password check into the new password's grant.
	return RepositoryHTTPSIdentity{UserID: u.Id, PasswordVersion: repositoryPasswordVersion(u)}, nil
}

// AuthenticateRepositoryHTTPSIdentity returns the identity/version belonging
// to this successful authentication. Existing provider authentication still
// runs on every new request; external-only provider resets have no local revision.
func AuthenticateRepositoryHTTPSIdentity(username, password string) (RepositoryHTTPSIdentity, error) {
	if username == "" {
		return RepositoryHTTPSIdentity{}, ErrInvalidCredential
	}
	if password != "" {
		u, err := UserSignIn(username, password)
		if err == nil {
			identity, err := repositoryPasswordIdentity(u)
			if err != nil {
				return RepositoryHTTPSIdentity{}, err
			}
			// Preserve the enabled-account recheck after a potentially slow provider
			// authentication, but never use this later row to replace the snapshot.
			if _, err = enabledAccount(u.Id); err != nil {
				return RepositoryHTTPSIdentity{}, err
			}
			return identity, nil
		} else if !errors.Is(err, ErrUserNotExist) {
			return RepositoryHTTPSIdentity{}, err
		}
	}
	token, err := GetAccessTokenBySha(username)
	if errors.Is(err, ErrAccessTokenNotExist) {
		return RepositoryHTTPSIdentity{}, ErrInvalidCredential
	} else if err != nil {
		return RepositoryHTTPSIdentity{}, err
	}
	if _, err = enabledAccount(token.Uid); err != nil {
		return RepositoryHTTPSIdentity{}, err
	}
	return RepositoryHTTPSIdentity{UserID: token.Uid, TokenID: token.Id}, nil
}

// AuthenticateRepositoryHTTPS preserves the legacy account/token-only API.
// Transport sessions must use AuthenticateRepositoryHTTPSIdentity instead.
func AuthenticateRepositoryHTTPS(username, password string) (userID, tokenID int64, err error) {
	identity, err := AuthenticateRepositoryHTTPSIdentity(username, password)
	return identity.UserID, identity.TokenID, err
}

// ValidateRepositoryPasswordVersion refuses a missing or changed local
// credential revision without retaining or reusing a plaintext password.
func ValidateRepositoryPasswordVersion(userID int64, version string) error {
	if version == "" {
		return ErrInvalidCredential
	}
	u, err := enabledAccount(userID)
	if err != nil {
		return err
	}
	if repositoryPasswordVersion(u) != version {
		return ErrInvalidCredential
	}
	return nil
}

// ValidateRepositoryCredential checks that the account is still enabled and
// that the key and token, when nonzero, still exist and belong to it.
func ValidateRepositoryCredential(userID, keyID, tokenID int64) error {
	if _, err := enabledAccount(userID); err != nil {
		return err
	}
	if keyID != 0 {
		if has, err := x.Where("id=?", keyID).And("owner_id=?", userID).Get(new(PublicKey)); err != nil {
			return err
		} else if !has {
			return ErrInvalidCredential
		}
	}
	if tokenID != 0 {
		if has, err := x.Where("id=?", tokenID).And("uid=?", userID).Get(new(AccessToken)); err != nil {
			return err
		} else if !has {
			return ErrInvalidCredential
		}
	}
	return nil
}
