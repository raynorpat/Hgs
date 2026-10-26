package models

import "testing"

func TestNewIssueUserPairsWithMultipleCollaborators(t *testing.T) {
	for _, posterIsCollaborator := range []bool{false, true} {
		t.Run(map[bool]string{false: "poster outside collaborators", true: "poster is collaborator"}[posterIsCollaborator], func(t *testing.T) {
			newTestDatabase(t)
			owner := newAccessTestUser(t, "owner", true)
			reader := newAccessTestUser(t, "reader", true)
			writer := newAccessTestUser(t, "writer", true)
			repo := newAccessTestRepo(t, owner, "issues", false)
			for _, access := range []*Access{{UserId: reader.Id, RepoId: repo.Id, Mode: READABLE}, {UserId: writer.Id, RepoId: repo.Id, Mode: WRITABLE}} {
				if err := AddAccess(access); err != nil {
					t.Fatal(err)
				}
			}
			poster := owner
			if posterIsCollaborator {
				poster = reader
			}
			issue := &Issue{RepoId: repo.Id, Index: 1, PosterId: poster.Id, Name: "multiple collaborators"}
			if err := NewIssue(issue); err != nil {
				t.Fatal(err)
			}
			if err := NewIssueUserPairs(repo.Id, issue.Id, owner.Id, poster.Id, writer.Id); err != nil {
				t.Fatal(err)
			}
			var pairs []IssueUser
			if err := x.Where("issue_id = ?", issue.Id).Find(&pairs); err != nil {
				t.Fatal(err)
			}
			want := 3
			if posterIsCollaborator {
				want = 2
			}
			if len(pairs) != want {
				t.Fatalf("got %d issue memberships, want %d", len(pairs), want)
			}
			ids := map[int64]bool{}
			users := map[int64]bool{reader.Id: false, writer.Id: false}
			if !posterIsCollaborator {
				users[owner.Id] = false
			}
			for _, pair := range pairs {
				if pair.Id == 0 || ids[pair.Id] {
					t.Fatal("issue-user row IDs not unique")
				}
				ids[pair.Id] = true
				seen, ok := users[pair.Uid]
				if !ok || seen {
					t.Fatalf("unexpected or duplicate recipient: %+v", pair)
				}
				users[pair.Uid] = true
				if pair.IsPoster != (pair.Uid == poster.Id) || pair.IsAssigned != (pair.Uid == writer.Id) {
					t.Fatalf("incorrect issue-user flags: %+v", pair)
				}
			}
		})
	}
}
