package models

import (
	"errors"
	"sort"

	"github.com/go-xorm/xorm"
)

// ForkRelation records the exact selected source; family identity never replaces
// the immediate-parent chain for authorization.
type ForkRelation struct {
	RepositoryID int64 `xorm:"pk 'repository_id'"`
	SourceID     int64 `xorm:"index 'source_id'"`
	FamilyID     int64 `xorm:"index 'family_id'"`
}

var ErrForkLineage = errors.New("invalid fork ancestry")
var ErrForkDependent = errors.New("repository has dependent forks")
var ErrForkPolicy = errors.New("fork lifecycle change is not permitted")

func GetForkRelation(repoID int64) (ForkRelation, error) {
	var r ForkRelation
	ok, err := x.Id(repoID).Get(&r)
	if err == nil && !ok {
		err = ErrRepoNotExist
	}
	return r, err
}
func ForkAncestors(repoID int64) ([]int64, error) { return forkAncestors(x, repoID, false) }

// policyGet uses locking current reads only inside the final authority transaction.
// SQLite callers acquire its write lock before any policy read. The ordinary UI
// and transports use exactly the same predicates without row locks.
func policyGet(e Engine, locked bool, table, where string, bean interface{}, args ...interface{}) (bool, error) {
	if locked && DbCfg.Type != "sqlite3" {
		return e.(*xorm.Session).Sql("SELECT * FROM `"+table+"` WHERE "+where+" FOR UPDATE", args...).Get(bean)
	}
	return e.Where(where, args...).Get(bean)
}
func forkAncestors(e Engine, repoID int64, locked bool) ([]int64, error) {
	ids := []int64{}
	seen := map[int64]bool{repoID: true}
	family := int64(0)
	for {
		var r ForkRelation
		ok, err := policyGet(e, locked, "fork_relation", "repository_id = ?", &r, repoID)
		if err != nil {
			return nil, err
		}
		if !ok {
			if family != 0 && family != repoID {
				return nil, ErrForkLineage
			}
			return ids, nil
		}
		if r.SourceID <= 0 || r.FamilyID <= 0 || seen[r.SourceID] || (family != 0 && family != r.FamilyID) {
			return nil, ErrForkLineage
		}
		family = r.FamilyID
		seen[r.SourceID] = true
		ids = append(ids, r.SourceID)
		repoID = r.SourceID
	}
}

func forkDependents(e Engine, id int64) error {
	if has, err := e.Where("source_id = ?", id).Get(new(ForkRelation)); err != nil {
		return err
	} else if has {
		return ErrForkDependent
	}
	return nil
}

// validateForkTransfer checks the new grant recipients rather than using the
// organization account as an ancestry bypass. Existing collaborators continue
// to pass the same central ancestor policy on every access.
func validateForkTransfer(sess *xorm.Session, repo *Repository, owner *User, team *Team) error {
	ids, err := forkAncestors(sess, repo.Id, true)
	if err != nil {
		return err
	}
	readers := map[int64]bool{}
	if !owner.IsOrganization() {
		readers[owner.Id] = true
	} else {
		if team == nil {
			return ErrForkPolicy
		}
		var current Team
		ok, err := policyGet(sess, true, "team", "id = ? AND org_id = ?", &current, team.Id, owner.Id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForkPolicy
		}
		var members []TeamUser
		var owners []OrgUser
		if DbCfg.Type == "sqlite3" {
			err = sess.Where("team_id = ?", team.Id).Find(&members)
			if err == nil {
				err = sess.Where("org_id = ? AND is_owner = ?", owner.Id, true).Find(&owners)
			}
		} else {
			err = sess.Sql("SELECT * FROM team_user WHERE team_id = ? ORDER BY uid FOR UPDATE", team.Id).Find(&members)
			if err == nil {
				err = sess.Sql("SELECT * FROM org_user WHERE org_id = ? AND is_owner = ? ORDER BY uid FOR UPDATE", owner.Id, true).Find(&owners)
			}
		}
		if err != nil {
			return err
		}
		current.Members = nil
		for _, m := range members {
			readers[m.Uid] = true
			current.Members = append(current.Members, &User{Id: m.Uid})
		}
		for _, m := range owners {
			readers[m.Uid] = true
		}
		*team = current
	}
	// Ordinary transfers must also use the current team snapshot.
	if len(ids) == 0 {
		return nil
	}
	ordered := make([]int64, 0, len(readers))
	for id := range readers {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	for _, id := range ids {
		var ancestor Repository
		ok, err := policyGet(sess, true, "repository", "id = ? AND state = ?", &ancestor, id, RepositoryReady)
		if err != nil {
			return err
		}
		if !ok {
			return ErrForkLineage
		}
		if !ancestor.IsPrivate {
			continue
		}
		for _, reader := range ordered {
			a, err := repositoryAccessFor(sess, reader, id, true)
			if err != nil {
				return err
			}
			if !a.Read {
				return ErrForkPolicy
			}
		}
	}
	return nil
}
