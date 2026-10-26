// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"path"
	"strings"
	"time"

	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/setting"
)

type ActionType int

const (
	CREATE_REPO ActionType = iota + 1 // 1
	DELETE_REPO                       // 2
	STAR_REPO                         // 3
	FOLLOW_REPO                       // 4
	_
	CREATE_ISSUE // 6
	_
	TRANSFER_REPO // 8
	_
	COMMENT_ISSUE // 10
)

// Action represents user operation type and other information to repository.,
// It is rendered by the dashboard activity feed.
type Action struct {
	Id           int64
	UserId       int64 // Receiver user id.
	OpType       ActionType
	ActUserId    int64  // Action user id.
	ActUserName  string // Action user name.
	ActEmail     string
	ActAvatar    string `xorm:"-"`
	RepoId       int64
	RepoUserName string
	RepoName     string
	IsPrivate    bool      `xorm:"NOT NULL DEFAULT false"`
	Content      string    `xorm:"TEXT"`
	Created      time.Time `xorm:"created"`
}

func (a Action) GetOpType() int {
	return int(a.OpType)
}

func (a Action) GetActUserName() string {
	return a.ActUserName
}

func (a Action) GetActEmail() string {
	return a.ActEmail
}

func (a Action) GetRepoUserName() string {
	return a.RepoUserName
}

func (a Action) GetRepoName() string {
	return a.RepoName
}

func (a Action) GetRepoLink() string {
	return path.Join("/", setting.AppSubUrl, a.RepoUserName, a.RepoName)
}

func (a Action) GetContent() string {
	return a.Content
}

func (a Action) GetCreate() time.Time {
	return a.Created
}

func (a Action) GetIssueInfos() []string {
	return strings.SplitN(a.Content, "|", 2)
}

// NewRepoAction adds new action for creating repository.
func NewRepoAction(u *User, repo *Repository) (err error) {
	if err = NotifyWatchers(&Action{ActUserId: u.Id, ActUserName: u.Name, ActEmail: u.Email,
		OpType: CREATE_REPO, RepoId: repo.Id, RepoUserName: repo.Owner.Name, RepoName: repo.Name,
		IsPrivate: repo.IsPrivate}); err != nil {
		log.Error(4, "NotifyWatchers: %d/%s", u.Id, repo.Name)
		return err
	}

	log.Trace("action.NewRepoAction: %s/%s", u.Name, repo.Name)
	return err
}

// TransferRepoAction adds new action for transferring repository.
func TransferRepoAction(u, newUser *User, repo *Repository) (err error) {
	action := &Action{
		ActUserId:    u.Id,
		ActUserName:  u.Name,
		ActEmail:     u.Email,
		OpType:       TRANSFER_REPO,
		RepoId:       repo.Id,
		RepoUserName: newUser.Name,
		RepoName:     repo.Name,
		IsPrivate:    repo.IsPrivate,
		Content:      path.Join(repo.Owner.LowerName, repo.LowerName),
	}
	if err = NotifyWatchers(action); err != nil {
		log.Error(4, "NotifyWatchers: %d/%s", u.Id, repo.Name)
		return err
	}

	// Remove watch for organization.
	if repo.Owner.IsOrganization() {
		if err = WatchRepo(repo.Owner.Id, repo.Id, false); err != nil {
			log.Error(4, "WatchRepo", err)
		}
	}

	log.Trace("action.TransferRepoAction: %s/%s", u.Name, repo.Name)
	return err
}

// GetFeeds returns action list of given user in given context.
func GetFeeds(uid, offset int64, isProfile bool) ([]*Action, error) {
	actions := make([]*Action, 0, 20)
	sess := x.Limit(20, int(offset)).Desc("id").Where("user_id=?", uid)
	if isProfile {
		sess.And("is_private=?", false).And("act_user_id=?", uid)
	}
	err := sess.Find(&actions)
	return actions, err
}
