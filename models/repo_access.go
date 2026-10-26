// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"errors"

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
	var access RepositoryAccess
	repo := new(Repository)
	has, err := x.Id(repoID).Where("state = ?", RepositoryReady).Get(repo)
	if err != nil {
		return access, err
	} else if !has {
		return access, ErrRepoNotExist
	}

	if userID == 0 {
		access.Read = !repo.IsPrivate && !setting.Service.RequireSignInView
		return access, nil
	}

	u, err := enabledAccount(userID)
	if err == ErrInvalidCredential {
		return access, nil
	} else if err != nil {
		return access, err
	}

	row := &Access{UserId: u.Id, RepoId: repo.Id}
	hasRow, err := x.Get(row)
	if err != nil {
		return access, err
	}
	access.Admin = repo.OwnerId == u.Id
	access.Write = hasRow && row.Mode >= WRITABLE
	access.Read = !repo.IsPrivate || hasRow

	owner := new(User)
	if has, err = x.Id(repo.OwnerId).Get(owner); err != nil {
		return RepositoryAccess{}, err
	} else if has && owner.IsOrganization() {
		// Organization owners administer the repository; admin-level teams only
		// get a WRITABLE row, as in RepoAssignment.
		if access.Admin, err = x.Where("uid=?", u.Id).And("org_id=?", owner.Id).
			And("is_owner=?", true).Get(new(OrgUser)); err != nil {
			return RepositoryAccess{}, err
		}
	}

	access.Write = access.Write || access.Admin
	access.Read = access.Read || access.Write
	return access, nil
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

// AuthenticateRepositoryHTTPS checks a username/password pair, or an access
// token given as the username, and returns the account and the token ID
// (0 for password authentication).
func AuthenticateRepositoryHTTPS(username, password string) (userID, tokenID int64, err error) {
	if len(username) == 0 {
		return 0, 0, ErrInvalidCredential
	}

	if len(password) > 0 {
		u, err := UserSignIn(username, password)
		if err == nil {
			if _, err = enabledAccount(u.Id); err != nil {
				return 0, 0, err
			}
			return u.Id, 0, nil
		} else if err != ErrUserNotExist {
			return 0, 0, err
		}
	}

	t, err := GetAccessTokenBySha(username)
	if err == ErrAccessTokenNotExist {
		return 0, 0, ErrInvalidCredential
	} else if err != nil {
		return 0, 0, err
	}
	if _, err = enabledAccount(t.Uid); err != nil {
		return 0, 0, err
	}
	return t.Uid, t.Id, nil
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
