// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"github.com/gogits/gogs/models"
	"testing"
)

func TestContributionPrivateAncestorAndRevocation(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, true)
	f.repo(t, 12, 1, false)
	if _, err := f.db.Insert(&models.ForkRelation{RepositoryID: 12, SourceID: 11, FamilyID: 11}, &models.Access{UserId: 1, RepoId: 11, Mode: models.READABLE}); err != nil {
		t.Fatal(err)
	}
	f.event(t, 12, "changeset", "2024-02-01T00:00:00Z")
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Changesets != 0 {
		t.Fatal("private ancestor bypass")
	}
	if p := f.read(t, 1, Scope{OwnerID: 1}, Query{}); p.Totals.Changesets != 1 {
		t.Fatal("authorized fork missing")
	}
	if _, err := f.db.Where("user_id = ? AND repo_id = ?", 1, 11).Delete(new(models.Access)); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 1, Scope{OwnerID: 1}, Query{}); p.Totals.Changesets != 0 {
		t.Fatal("revoked ancestor reader retained grant")
	}
}

func TestContributionLifecycleOwnershipAndProof(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 12, 3, false)
	e := f.event(t, 12, "changeset", "2024-02-01T00:00:00Z")
	if _, err := f.db.Id(e.ID).Cols("actor_id").Update(&models.ContributionEvent{ActorID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Insert(&models.EmailAddress{Uid: 2, Email: " ALICE@example.invalid ", IsActivated: true}); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Changesets != 0 {
		t.Fatal("ambiguous proof/stored actor credited")
	}
	if p := f.read(t, 0, Scope{OwnerID: 3, Organization: true}, Query{}); p.Totals.Changesets != 1 {
		t.Fatal("org excluded nonmember/unattributed author")
	}
	if _, err := f.db.Id(12).Cols("owner_id", "name").Update(&models.Repository{OwnerId: 2, Name: "renamed"}); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 3, Organization: true}, Query{}); p.Totals.Changesets != 0 {
		t.Fatal("transferred repo remained in old organization")
	}
	if _, err := f.db.Id(1).Delete(new(models.User)); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals != (Counts{}) {
		t.Fatal("deleted account retained attribution")
	}
}

func TestContributionLifecycleRetainedPull(t *testing.T) {
	f := newQueryFixture(t)
	f.repo(t, 11, 2, true)
	e := f.event(t, 10, "pull", "2024-02-01T00:00:00Z")
	if _, err := f.db.Id(e.ObjectID).Cols("source_repo_id").Update(&models.PullRequest{SourceRepoID: 11}); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Pulls != 0 {
		t.Fatal("private PR source bypass")
	}
	if _, err := f.db.Id(e.ObjectID).Cols("source_deleted", "state").Update(&models.PullRequest{SourceDeleted: true, State: models.PullMerged}); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Pulls != 0 {
		t.Fatal("source-deleted flag accepted before finalized deletion")
	}
	if _, err := f.db.Id(11).Delete(new(models.Repository)); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Pulls != 1 {
		t.Fatal("retained merged history missing")
	}
	if _, err := f.db.Id(e.ObjectID).Cols("state").Update(&models.PullRequest{State: models.PullDeclined}); err != nil {
		t.Fatal(err)
	}
	if p := f.read(t, 0, Scope{OwnerID: 1}, Query{}); p.Totals.Pulls != 0 {
		t.Fatal("unmerged deleted-source history leaked")
	}
}
