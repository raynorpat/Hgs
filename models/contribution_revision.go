// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"crypto/sha256"
	"fmt"

	"github.com/go-xorm/xorm"
)

// ContributionClock serializes revisions, but its value is never a public
// watermark. Only eligible visibility classes contribute to a query revision.
type ContributionClock struct {
	ID       int64 `xorm:"pk 'id'"`
	Revision int64
}

// Classes survive removal of their last event. This tombstone makes deletion
// of a nonmaximum event observable without loading event history. SourceID zero
// denotes an ordinary repository event or finalized retained merged history.
type ContributionClass struct {
	Key          string `xorm:"pk VARCHAR(64)"`
	RepositoryID int64  `xorm:"index 'repository_id'"`
	SourceID     int64  `xorm:"index 'source_id'"`
	ActorID      int64  `xorm:"index 'actor_id'"`
	AuthorEmail  string `xorm:"VARCHAR(254) index" json:"-"`
	Kind         string `xorm:"VARCHAR(16)"`
	Revision     int64
}

func nextContributionRevision(e Engine) (int64, error) {
	r, err := e.Exec("UPDATE contribution_clock SET revision = revision + 1 WHERE id = 1")
	if err != nil {
		return 0, err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		if _, err = e.Insert(&ContributionClock{ID: 1, Revision: 1}); err != nil {
			return 0, err
		}
	}
	var clock ContributionClock
	_, err = e.Id(1).Get(&clock)
	return clock.Revision, err
}

func contributionClass(repo, source int64, event ContributionEvent) ContributionClass {
	c := ContributionClass{RepositoryID: repo, SourceID: source, ActorID: event.ActorID, AuthorEmail: event.AuthorEmail, Kind: event.Kind}
	if event.Kind == "changeset" {
		c.ActorID = 0
	}
	c.Key = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%d/%s/%s", repo, source, c.ActorID, c.Kind, c.AuthorEmail))))
	return c
}

func ensureContributionClass(e Engine, c ContributionClass) error {
	var old ContributionClass
	ok, err := e.Id(c.Key).Get(&old)
	if err != nil {
		return err
	}
	if !ok {
		_, err = e.Insert(&c)
	} else if c.Revision > old.Revision {
		_, err = e.Id(c.Key).Cols("revision").Update(&c)
	}
	return err
}

func stampContributionClass(e Engine, repo, source int64, event ContributionEvent) error {
	revision, err := nextContributionRevision(e)
	if err != nil {
		return err
	}
	c := contributionClass(repo, source, event)
	c.Revision = revision
	return ensureContributionClass(e, c)
}

// stampContributionObject runs inside the canonical mutation transaction.
// Application objects have bounded pages of actors (one opening plus reviews).
func stampContributionObject(s *xorm.Session, kind string, id int64) error {
	for after := int64(0); ; {
		var events []ContributionEvent
		predicate := "kind = ? AND object_id = ? AND id > ?"
		if kind == "pull" {
			predicate = "kind IN ('pull','review') AND object_id = ? AND id > ?"
		}
		q := s.Where(predicate, kind, id, after)
		if kind == "pull" {
			q = s.Where(predicate, id, after)
		}
		if err := q.Asc("id").Limit(100).Find(&events); err != nil {
			return err
		}
		for _, event := range events {
			after = event.ID
			var memberships []ContributionRepository
			if err := s.Where("event_id = ?", event.ID).Find(&memberships); err != nil {
				return err
			}
			source := int64(0)
			if kind == "pull" {
				var p PullRequest
				if _, err := s.Id(id).Get(&p); err != nil {
					return err
				}
				if !p.SourceDeleted {
					source = p.SourceRepoID
				}
			}
			for _, m := range memberships {
				if err := stampContributionClass(s, m.RepositoryID, source, event); err != nil {
					return err
				}
			}
		}
		if len(events) < 100 {
			return nil
		}
	}
}
