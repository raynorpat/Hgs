// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"errors"
	"fmt"
	"github.com/Unknwon/com"
	"html/template"
	"regexp"
	"strings"
	"time"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/log"
	"github.com/gogits/gogs/modules/setting"
)

var (
	ErrRepoAlreadyExist  = errors.New("Repository already exist")
	ErrRepoNotExist      = errors.New("Repository does not exist")
	ErrRepoFileNotExist  = errors.New("Repository file does not exist")
	ErrRepoNameIllegal   = errors.New("Repository name contains illegal characters")
	ErrRepoFileNotLoaded = errors.New("Repository file not loaded")
	ErrInvalidReference  = errors.New("Invalid reference specified")
)

var (
	DescPattern = regexp.MustCompile(`https?://\S+`)
)

// Repository represents a Mercurial repository.
type Repository struct {
	Id          int64
	OwnerId     int64  `xorm:"UNIQUE(s)"`
	Owner       *User  `xorm:"-"`
	LowerName   string `xorm:"UNIQUE(s) INDEX NOT NULL"`
	Name        string `xorm:"INDEX NOT NULL"`
	Description string
	Website     string

	NumWatches          int
	NumStars            int
	NumIssues           int
	NumClosedIssues     int
	NumOpenIssues       int `xorm:"-"`
	NumMilestones       int `xorm:"NOT NULL DEFAULT 0"`
	NumClosedMilestones int `xorm:"NOT NULL DEFAULT 0"`
	NumOpenMilestones   int `xorm:"-"`

	IsPrivate bool

	// State and StorageNonce belong to the storage lifecycle and change
	// only through repo_lifecycle.go.
	State        RepositoryState `xorm:"VARCHAR(16) INDEX NOT NULL"`
	StorageNonce string          `xorm:"VARCHAR(32) NOT NULL"`

	Created time.Time `xorm:"CREATED"`
	Updated time.Time `xorm:"UPDATED"`
}

func (repo *Repository) GetOwner() (err error) {
	if repo.Owner == nil {
		repo.Owner, err = GetUserById(repo.OwnerId)
	}
	return err
}

func (repo *Repository) RepoLink() (string, error) {
	if err := repo.GetOwner(); err != nil {
		return "", err
	}
	return setting.AppSubUrl + "/" + repo.Owner.Name + "/" + repo.Name, nil
}

func (repo *Repository) IsOwnedBy(u *User) bool {
	return repo.OwnerId == u.Id
}

func (repo *Repository) HasAccess(uname string) bool {
	u, err := GetUserByName(uname)
	if err != nil {
		return false
	}
	has, _ := HasAccess(u.Id, repo.Id, READABLE)
	return has
}

// DescriptionHtml does special handles to description and return HTML string.
func (repo *Repository) DescriptionHtml() template.HTML {
	sanitize := func(s string) string {
		return fmt.Sprintf(`<a href="%[1]s" target="_blank">%[1]s</a>`, s)
	}
	return template.HTML(DescPattern.ReplaceAllStringFunc(base.Sanitizer.Sanitize(repo.Description), sanitize))
}

// IsRepositoryExist returns true if the name under user is taken by a
// repository in any lifecycle state.
func IsRepositoryExist(u *User, repoName string) (bool, error) {
	return x.Where("lower_name = ?", strings.ToLower(repoName)).Get(&Repository{OwnerId: u.Id})
}

// CloneLink represents different types of clone URLs of repository.
type CloneLink struct {
	SSH   string
	HTTPS string
}

var (
	illegalEquals  = []string{"hg", "debug", "raw", "install", "api", "avatar", "user", "org", "help", "stars", "issues", "pulls", "commits", "repo", "template", "admin", "new"}
	illegalSuffixs = []string{".git", ".keys"}
)

// IsLegalName returns false if name contains illegal characters.
func IsLegalName(repoName string) bool {
	repoName = strings.ToLower(repoName)
	for _, char := range illegalEquals {
		if repoName == char {
			return false
		}
	}
	for _, char := range illegalSuffixs {
		if strings.HasSuffix(repoName, char) {
			return false
		}
	}
	return true
}

// CountRepositories returns number of repositories.
func CountRepositories() int64 {
	count, _ := x.Where("state = ?", RepositoryReady).Count(new(Repository))
	return count
}

// GetRepositoriesWithUsers returns given number of repository objects with offset.
// It also auto-gets corresponding users.
func GetRepositoriesWithUsers(num, offset int) ([]*Repository, error) {
	repos := make([]*Repository, 0, num)
	if err := x.Where("state = ?", RepositoryReady).Limit(num, offset).Asc("id").Find(&repos); err != nil {
		return nil, err
	}

	for _, repo := range repos {
		repo.Owner = &User{Id: repo.OwnerId}
		has, err := x.Get(repo.Owner)
		if err != nil {
			return nil, err
		} else if !has {
			return nil, ErrUserNotExist
		}
	}

	return repos, nil
}

// TransferOwnership transfers all corresponding setting from old user to new one.
func TransferOwnership(u *User, newOwner string, repo *Repository) error {
	newUser, err := GetUserByName(newOwner)
	if err != nil {
		return fmt.Errorf("fail to get new owner(%s): %v", newOwner, err)
	}

	// Check if new owner has repository with same name.
	has, err := IsRepositoryExist(newUser, repo.Name)
	if err != nil {
		return err
	} else if has {
		return ErrRepoAlreadyExist
	}

	// Resolve the owner-team identity here. Its mutable metadata and members
	// are reloaded under the transaction's team lock before use.
	var t *Team
	if newUser.IsOrganization() {
		if t, err = newUser.GetOwnerTeam(); err != nil {
			return err
		}
	}

	lifecycleStage("transfer-before-transaction")
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	defer sess.Rollback()
	if err = lockReadyReviewRepository(sess, repo.Id); err != nil {
		if errors.Is(err, ErrReviewReserved) {
			return ErrRepoNotExist
		}
		return err
	}
	var current Repository
	if ok, e := policyGet(sess, true, "repository", "id = ? AND state = ?", &current, repo.Id, RepositoryReady); e != nil {
		return e
	} else if !ok || current.OwnerId != repo.OwnerId || current.IsPrivate != repo.IsPrivate {
		return ErrForkPolicy
	}
	if err = forkDependents(sess, repo.Id); err != nil {
		return err
	}
	if reserved, e := forkReserved(sess, repo.Id); e != nil {
		return e
	} else if reserved {
		return ErrReviewReserved
	}
	if reserved, e := sess.Id(repo.Id).Get(new(ReviewReservation)); e != nil {
		return e
	} else if reserved {
		return ErrReviewReserved
	}
	if err = validateForkTransfer(sess, repo, newUser, t); err != nil {
		return err
	}
	lifecycleStage("transfer-authorized")
	owner := repo.Owner
	// Delete all access first if current owner is an organization.
	if owner.IsOrganization() {
		if _, err = sess.Where("repo_id=?", repo.Id).Delete(new(Access)); err != nil {
			sess.Rollback()
			return fmt.Errorf("fail to delete current accesses: %v", err)
		}
	} else {
		// Delete current owner access.
		if _, err = sess.Where("repo_id=?", repo.Id).And("user_id=?", owner.Id).
			Delete(new(Access)); err != nil {
			sess.Rollback()
			return fmt.Errorf("fail to delete access(owner): %v", err)
		}
		// In case new owner has access.
		if _, err = sess.Where("repo_id=?", repo.Id).And("user_id=?", newUser.Id).
			Delete(new(Access)); err != nil {
			sess.Rollback()
			return fmt.Errorf("fail to delete access(new user): %v", err)
		}
	}

	// Update repository. Only the owner column: the loaded row is stale.
	repo.OwnerId = newUser.Id
	if n, err := sess.Id(repo.Id).Where("state = ?", RepositoryReady).Cols("owner_id").Update(&Repository{OwnerId: newUser.Id}); err != nil {
		sess.Rollback()
		return err
	} else if n != 1 {
		// Deleted or being deleted since the caller loaded it.
		sess.Rollback()
		return ErrRepoNotExist
	}

	// Update user repository number.
	if _, err = sess.Exec("UPDATE `user` SET num_repos = num_repos + 1 WHERE id = ?", newUser.Id); err != nil {
		sess.Rollback()
		return err
	}

	if _, err = sess.Exec("UPDATE `user` SET num_repos = num_repos - 1 WHERE id = ?", owner.Id); err != nil {
		sess.Rollback()
		return err
	}

	mode := WRITABLE
	// New owner is organization.
	if newUser.IsOrganization() {
		access := &Access{
			RepoId: repo.Id,
			Mode:   mode,
		}

		// Give access to all members in owner team.
		for _, u := range t.Members {
			access.Id = 0
			access.UserId = u.Id
			if _, err = sess.Insert(access); err != nil {
				sess.Rollback()
				return err
			}
		}

		// Update owner team info and count.
		t.RepoIds += "$" + com.ToStr(repo.Id) + "|"
		t.NumRepos++
		if _, err = sess.Id(t.Id).Cols("repo_ids", "num_repos").Update(t); err != nil {
			sess.Rollback()
			return err
		}
	} else {
		access := &Access{
			UserId: newUser.Id,
			RepoId: repo.Id,
			Mode:   mode,
		}
		if _, err = sess.Insert(access); err != nil {
			sess.Rollback()
			return fmt.Errorf("fail to insert access: %v", err)
		}
	}

	if err = commitRetiringReviews(sess); err != nil {
		return err
	}

	if err = WatchRepo(newUser.Id, repo.Id, true); err != nil {
		log.Error(4, "WatchRepo", err)
	}

	if err = TransferRepoAction(u, newUser, repo); err != nil {
		return err
	}

	return nil
}

// ChangeRepositoryName renames a ready repository. Storage is keyed by ID,
// so nothing on disk or in access rows changes.
func ChangeRepositoryName(repoID int64, newRepoName string) error {
	if !isValidRepoName(newRepoName) {
		return ErrRepoNameIllegal
	}
	repo, err := GetRepositoryById(repoID)
	if err != nil {
		return err
	}
	if has, err := x.Where("owner_id = ?", repo.OwnerId).And("lower_name = ?", strings.ToLower(newRepoName)).
		And("id != ?", repoID).Get(new(Repository)); err != nil {
		return err
	} else if has {
		return ErrRepoAlreadyExist
	}
	_, err = x.Exec("UPDATE `repository` SET name = ?, lower_name = ? WHERE id = ? AND state = ?",
		newRepoName, strings.ToLower(newRepoName), repoID, RepositoryReady)
	return err
}

// UpdateRepository saves a repository loaded from the database. Rows that
// are no longer ready are left unchanged.
func UpdateRepository(repo *Repository) error {
	repo.LowerName = strings.ToLower(repo.Name)

	if len(repo.Description) > 255 {
		repo.Description = repo.Description[:255]
	}
	if len(repo.Website) > 255 {
		repo.Website = repo.Website[:255]
	}
	sess := x.NewSession()
	defer sess.Close()
	if err := sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	if err := lockReadyReviewRepository(sess, repo.Id); err != nil {
		return err
	}
	var current Repository
	if _, err := policyGet(sess, true, "repository", "id = ?", &current, repo.Id); err != nil {
		return err
	}
	if current.OwnerId != repo.OwnerId || current.State != repo.State || current.StorageNonce != repo.StorageNonce {
		return ErrForkPolicy
	}
	if current.IsPrivate != repo.IsPrivate {
		if err := forkDependents(sess, repo.Id); err != nil {
			return err
		}
		if reserved, err := forkReserved(sess, repo.Id); err != nil {
			return err
		} else if reserved {
			return ErrReviewReserved
		}
		if reserved, err := sess.Id(repo.Id).Get(new(ReviewReservation)); err != nil {
			return err
		} else if reserved {
			return ErrReviewReserved
		}
		if !repo.IsPrivate {
			ids, err := forkAncestors(sess, repo.Id, true)
			if err != nil {
				return err
			}
			for _, id := range ids {
				var parent Repository
				has, err := policyGet(sess, true, "repository", "id = ? AND state = ?", &parent, id, RepositoryReady)
				if err != nil {
					return err
				}
				if !has || parent.IsPrivate {
					return ErrForkPolicy
				}
			}
		}
	}
	// Storage identity, ownership and lifecycle never travel through metadata saves.
	if _, err := sess.Id(repo.Id).Cols("name", "lower_name", "description", "website", "is_private").Update(repo); err != nil {
		return err
	}
	return commitRetiringReviews(sess)
}

// DeleteRepository deletes the database rows of a repository for a user or
// organization. Only DeleteHgRepository removes repository storage.
func DeleteRepository(uid, repoId int64, userName string) error {
	repo := &Repository{Id: repoId, OwnerId: uid}
	has, err := x.Get(repo)
	if err != nil {
		return err
	} else if !has {
		return ErrRepoNotExist
	}
	if repo.State == RepositoryReady {
		if err = tombstoneReviewSafe(repo.Id); err != nil {
			return err
		}
		repo.State = RepositoryDeleting
	}
	return deleteRepositoryRows(repo)
}

// deleteRepositoryRows deletes a repository row and its related rows.
func deleteRepositoryRows(repo *Repository) error {
	uid, repoId := repo.OwnerId, repo.Id

	// In case is a organization.
	org, err := GetUserById(uid)
	if err != nil {
		return err
	}
	if org.IsOrganization() {
		if err = org.GetTeams(); err != nil {
			return err
		}
	}

	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}

	if err = forkDependents(sess, repoId); err != nil {
		sess.Rollback()
		return err
	}
	if reserved, e := forkReserved(sess, repoId); e != nil {
		sess.Rollback()
		return e
	} else if reserved {
		sess.Rollback()
		return ErrReviewReserved
	}
	if _, err = sess.Id(repoId).Delete(new(ForkRelation)); err != nil {
		sess.Rollback()
		return err
	}
	if _, err = sess.Delete(&Repository{Id: repoId}); err != nil {
		sess.Rollback()
		return err
	}

	// Delete all access.
	if _, err := sess.Delete(&Access{RepoId: repo.Id}); err != nil {
		sess.Rollback()
		return err
	}
	if org.IsOrganization() {
		idStr := "$" + com.ToStr(repoId) + "|"
		for _, t := range org.Teams {
			if !strings.Contains(t.RepoIds, idStr) {
				continue
			}
			t.NumRepos--
			t.RepoIds = strings.Replace(t.RepoIds, idStr, "", 1)
			if _, err = sess.Id(t.Id).AllCols().Update(t); err != nil {
				sess.Rollback()
				return err
			}
		}
	}

	if _, err := sess.Delete(&Action{RepoId: repo.Id}); err != nil {
		sess.Rollback()
		return err
	}
	if _, err = sess.Delete(&Watch{RepoId: repoId}); err != nil {
		sess.Rollback()
		return err
	}
	if _, err = sess.Delete(&IssueUser{RepoId: repoId}); err != nil {
		sess.Rollback()
		return err
	}
	if _, err = sess.Delete(&Milestone{RepoId: repoId}); err != nil {
		sess.Rollback()
		return err
	}

	// Delete comments.
	if err = x.Iterate(&Issue{RepoId: repoId}, func(idx int, bean interface{}) error {
		issue := bean.(*Issue)
		if _, err = sess.Delete(&Comment{IssueId: issue.Id}); err != nil {
			sess.Rollback()
			return err
		}
		return nil
	}); err != nil {
		sess.Rollback()
		return err
	}

	if _, err = sess.Delete(&Issue{RepoId: repoId}); err != nil {
		sess.Rollback()
		return err
	}

	if _, err = sess.Exec("UPDATE `user` SET num_repos = num_repos - 1 WHERE id = ?", uid); err != nil {
		sess.Rollback()
		return err
	}

	return sess.Commit()
}

// GetRepositoryByRef returns a Repository specified by a GFM reference.
// See https://help.github.com/articles/writing-on-github#references for more information on the syntax.
func GetRepositoryByRef(ref string) (*Repository, error) {
	n := strings.IndexByte(ref, byte('/'))

	if n < 2 {
		return nil, ErrInvalidReference
	}

	userName, repoName := ref[:n], ref[n+1:]

	user, err := GetUserByName(userName)

	if err != nil {
		return nil, err
	}

	return GetRepositoryByName(user.Id, repoName)
}

// GetRepositoryByName returns the repository by given name under user if exists.
func GetRepositoryByName(uid int64, repoName string) (*Repository, error) {
	repo := &Repository{
		OwnerId:   uid,
		LowerName: strings.ToLower(repoName),
	}
	has, err := x.Where("state = ?", RepositoryReady).Get(repo)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrRepoNotExist
	}
	return repo, err
}

// GetRepositoryById returns the repository by given id if exists.
func GetRepositoryById(id int64) (*Repository, error) {
	repo := &Repository{}
	has, err := x.Id(id).Where("state = ?", RepositoryReady).Get(repo)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrRepoNotExist
	}
	return repo, nil
}

// GetRepositories returns a list of repositories of given user.
func GetRepositories(uid int64, private bool) ([]*Repository, error) {
	repos := make([]*Repository, 0, 10)
	sess := x.Where("state = ?", RepositoryReady).Desc("updated")
	if !private {
		sess.And("is_private=?", false)
	}

	err := sess.Find(&repos, &Repository{OwnerId: uid})
	return repos, err
}

// GetRecentUpdatedRepositories returns the list of repositories that are recently updated.
func GetRecentUpdatedRepositories(num int) (repos []*Repository, err error) {
	err = x.Where("is_private=?", false).And("state = ?", RepositoryReady).Limit(num).Desc("updated").Find(&repos)
	return repos, err
}

// GetRepositoryCount returns the total number of repositories of user.
func GetRepositoryCount(user *User) (int64, error) {
	return x.Count(&Repository{OwnerId: user.Id})
}

// CollaborativeRepository represents a repository with collaborative information.
type CollaborativeRepository struct {
	*Repository
	CanPush bool
}

// GetCollaborativeRepos returns a list of repositories that user is collaborator.
func GetCollaborativeRepos(uid int64) ([]*CollaborativeRepository, error) {
	accesses := make([]*Access, 0, 10)
	if err := x.Find(&accesses, &Access{UserId: uid}); err != nil {
		return nil, err
	}

	repos := make([]*CollaborativeRepository, 0, 10)
	for _, access := range accesses {
		repo, err := GetRepositoryById(access.RepoId)
		if err == ErrRepoNotExist {
			continue
		} else if err != nil {
			return nil, err
		} else if repo.OwnerId == uid {
			continue
		} else if err = repo.GetOwner(); err != nil {
			return nil, err
		}
		repos = append(repos, &CollaborativeRepository{repo, access.Mode == WRITABLE})
	}
	return repos, nil
}

// GetCollaborators returns a list of users of repository's collaborators.
func GetCollaborators(repoId int64) (us []*User, err error) {
	accesses := make([]*Access, 0, 10)
	if err = x.Find(&accesses, &Access{RepoId: repoId}); err != nil {
		return nil, err
	}

	us = make([]*User, len(accesses))
	for i := range accesses {
		us[i], err = GetUserById(accesses[i].UserId)
		if err != nil {
			return nil, err
		}
	}
	return us, nil
}

type SearchOption struct {
	Keyword string
	Uid     int64
	Limit   int
	Private bool
}

// SearchRepositoryByName returns given number of repositories whose name contains keyword.
func SearchRepositoryByName(opt SearchOption) (repos []*Repository, err error) {
	if len(opt.Keyword) == 0 {
		return repos, nil
	}
	opt.Keyword = strings.ToLower(opt.Keyword)

	repos = make([]*Repository, 0, opt.Limit)

	// Append conditions.
	sess := x.Where("state = ?", RepositoryReady).Limit(opt.Limit)
	if opt.Uid > 0 {
		sess.And("owner_id=?", opt.Uid)
	}
	if !opt.Private {
		sess.And("is_private=false")
	}
	sess.And("lower_name like ?", "%"+opt.Keyword+"%").Find(&repos)
	return repos, err
}

type Watch struct {
	Id     int64
	UserId int64 `xorm:"UNIQUE(watch)"`
	RepoId int64 `xorm:"UNIQUE(watch)"`
}

// IsWatching checks if user has watched given repository.
func IsWatching(uid, repoId int64) bool {
	has, _ := x.Get(&Watch{0, uid, repoId})
	return has
}

func watchRepo(e Engine, uid, repoId int64, watch bool) (err error) {
	if watch {
		if IsWatching(uid, repoId) {
			return nil
		}
		if _, err = e.Insert(&Watch{RepoId: repoId, UserId: uid}); err != nil {
			return err
		}
		_, err = e.Exec("UPDATE `repository` SET num_watches = num_watches + 1 WHERE id = ?", repoId)
	} else {
		if !IsWatching(uid, repoId) {
			return nil
		}
		if _, err = e.Delete(&Watch{0, uid, repoId}); err != nil {
			return err
		}
		_, err = e.Exec("UPDATE `repository` SET num_watches = num_watches - 1 WHERE id = ?", repoId)
	}
	return err
}

// Watch or unwatch repository.
func WatchRepo(uid, repoId int64, watch bool) (err error) {
	return watchRepo(x, uid, repoId, watch)
}

// GetWatchers returns all watchers of given repository.
func GetWatchers(rid int64) ([]*Watch, error) {
	watches := make([]*Watch, 0, 10)
	err := x.Find(&watches, &Watch{RepoId: rid})
	return watches, err
}

// NotifyWatchers creates batch of actions for every watcher.
func NotifyWatchers(act *Action) error {
	// Add feeds for user self and all watchers.
	watches, err := GetWatchers(act.RepoId)
	if err != nil {
		return errors.New("repo.NotifyWatchers(get watches): " + err.Error())
	}

	// Add feed for actioner.
	act.UserId = act.ActUserId
	if _, err = x.InsertOne(act); err != nil {
		return errors.New("repo.NotifyWatchers(create action): " + err.Error())
	}

	for i := range watches {
		if act.ActUserId == watches[i].UserId {
			continue
		}

		act.Id = 0
		act.UserId = watches[i].UserId
		if _, err = x.InsertOne(act); err != nil {
			return errors.New("repo.NotifyWatchers(create action): " + err.Error())
		}
	}
	return nil
}

//   _________ __
//  /   _____//  |______ _______
//  \_____  \\   __\__  \\_  __ \
//  /        \|  |  / __ \|  | \/
// /_______  /|__| (____  /__|
//         \/           \/

type Star struct {
	Id     int64
	Uid    int64 `xorm:"UNIQUE(s)"`
	RepoId int64 `xorm:"UNIQUE(s)"`
}

// Star or unstar repository.
func StarRepo(uid, repoId int64, star bool) (err error) {
	if star {
		if IsStaring(uid, repoId) {
			return nil
		}
		if _, err = x.Insert(&Star{Uid: uid, RepoId: repoId}); err != nil {
			return err
		} else if _, err = x.Exec("UPDATE `repository` SET num_stars = num_stars + 1 WHERE id = ?", repoId); err != nil {
			return err
		}
		_, err = x.Exec("UPDATE `user` SET num_stars = num_stars + 1 WHERE id = ?", uid)
	} else {
		if !IsStaring(uid, repoId) {
			return nil
		}
		if _, err = x.Delete(&Star{0, uid, repoId}); err != nil {
			return err
		} else if _, err = x.Exec("UPDATE `repository` SET num_stars = num_stars - 1 WHERE id = ?", repoId); err != nil {
			return err
		}
		_, err = x.Exec("UPDATE `user` SET num_stars = num_stars - 1 WHERE id = ?", uid)
	}
	return err
}

// IsStaring checks if user has starred given repository.
func IsStaring(uid, repoId int64) bool {
	has, _ := x.Get(&Star{0, uid, repoId})
	return has
}
