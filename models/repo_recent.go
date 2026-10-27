package models

import (
	"context"
	"sync"
	"time"
)

// RepositoryVisit stores identity only; names and permissions are read live.
type RepositoryVisit struct {
	Id        int64
	ActorID   int64 `xorm:"'actor_id' UNIQUE(account_repo) INDEX NOT NULL"`
	RepoID    int64 `xorm:"'repo_id' UNIQUE(account_repo) NOT NULL"`
	VisitedAt int64 `xorm:"NOT NULL"`
}

// Serialize visit/clear transactions in this process; the database unique index
// independently prevents duplicate account/repository identities.
var repositoryVisitsMu sync.Mutex

func RecordRepositoryVisit(ctx context.Context, actorID, repoID int64, at time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actorID <= 0 {
		return nil
	}
	access, err := RepositoryAccessFor(actorID, repoID)
	if err == ErrRepoNotExist {
		return nil
	}
	if err != nil {
		return err
	}
	if !access.Read {
		return nil
	}
	repositoryVisitsMu.Lock()
	defer repositoryVisitsMu.Unlock()
	sess := x.NewSession()
	defer sess.Close()
	if err = sess.Begin(); err != nil {
		return err
	}
	defer sess.Rollback()
	visit := &RepositoryVisit{}
	has, err := sess.Where("actor_id=?", actorID).And("repo_id=?", repoID).Get(visit)
	if err != nil {
		return err
	}
	if !has {
		visit = &RepositoryVisit{ActorID: actorID, RepoID: repoID, VisitedAt: at.UTC().UnixNano()}
		_, err = sess.Insert(visit)
	} else if at.UnixNano() > visit.VisitedAt {
		_, err = sess.Id(visit.Id).Cols("visited_at").Update(&RepositoryVisit{VisitedAt: at.UTC().UnixNano()})
	}
	if err != nil {
		return err
	}
	var rows []*RepositoryVisit
	if err = sess.Where("actor_id=?", actorID).Desc("visited_at", "id").Find(&rows); err != nil {
		return err
	}
	for _, row := range rows[min(20, len(rows)):] {
		if _, err = sess.Id(row.Id).Delete(new(RepositoryVisit)); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	return sess.Commit()
}

func RecentRepositories(ctx context.Context, actorID int64) ([]*Repository, error) {
	result := []*Repository{}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := enabledAccount(actorID); err == ErrInvalidCredential {
		return result, nil
	} else if err != nil {
		return nil, err
	}
	var rows []*RepositoryVisit
	if err := x.Where("actor_id=?", actorID).Desc("visited_at", "id").Limit(20).Find(&rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		access, err := RepositoryAccessFor(actorID, row.RepoID)
		if err == ErrRepoNotExist {
			continue
		}
		if err != nil {
			return nil, err
		}
		if !access.Read {
			continue
		}
		repo, err := GetRepositoryById(row.RepoID)
		if err == ErrRepoNotExist {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err = repo.GetOwner(); err == ErrUserNotExist {
			continue
		} else if err != nil {
			return nil, err
		}
		result = append(result, repo)
		if len(result) == 10 {
			break
		}
	}
	return result, nil
}

func ClearRepositoryVisits(ctx context.Context, actorID int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if actorID <= 0 {
		return nil
	}
	repositoryVisitsMu.Lock()
	defer repositoryVisitsMu.Unlock()
	_, err := x.Where("actor_id=?", actorID).Delete(new(RepositoryVisit))
	return err
}
