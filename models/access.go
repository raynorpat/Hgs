// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"errors"
	"time"
)

type AccessType int

const (
	READABLE AccessType = iota + 1
	WRITABLE
)

// Access represents the accessibility of user to repository.
type Access struct {
	Id      int64
	UserId  int64      `xorm:"UNIQUE(s) INDEX NOT NULL"`
	RepoId  int64      `xorm:"UNIQUE(s) INDEX NOT NULL"`
	Mode    AccessType `xorm:"NOT NULL"`
	Created time.Time  `xorm:"CREATED"`
}

// AddAccess adds new access record, or changes the mode of an existing one.
func AddAccess(access *Access) error {
	sess := x.NewSession()
	defer sess.Close()
	defer sess.Rollback()
	if err := sess.Begin(); err != nil {
		return err
	}
	if err := addAccessWithAuthorize(sess, &Access{UserId: access.UserId, RepoId: access.RepoId}, access.Mode); err != nil {
		return err
	}
	return commitRetiringReviews(sess)
}

// DeleteAccess deletes access record of given user and repository.
func DeleteAccess(access *Access) error {
	if access.UserId == 0 || access.RepoId == 0 {
		return errors.New("access user and repository must be given")
	}
	sess := x.NewSession()
	defer sess.Close()
	defer sess.Rollback()
	if err := sess.Begin(); err != nil {
		return err
	}
	if _, err := sess.Delete(&Access{UserId: access.UserId, RepoId: access.RepoId}); err != nil {
		return err
	}
	return commitRetiringReviews(sess)
}

// HasAccess returns true if user has at least given mode of access record to repository.
func HasAccess(userId, repoId int64, mode AccessType) (bool, error) {
	if userId == 0 || repoId == 0 {
		return false, nil
	}
	access := &Access{UserId: userId, RepoId: repoId}
	has, err := x.Get(access)
	if err != nil || !has {
		return false, err
	}
	return mode <= access.Mode, nil
}
