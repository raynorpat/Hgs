package models

import "testing"

func TestFreshDatabasePreservesUsersAndIssues(t *testing.T) {
	reload := newTestDatabase(t)

	user := &User{Name: "alice", Email: "alice@example.com", Passwd: "secret"}
	if err := CreateUser(user); err != nil {
		t.Fatal(err)
	}
	repo := &Repository{OwnerId: user.Id, Name: "demo", LowerName: "demo"}
	if _, err := x.Insert(repo); err != nil {
		t.Fatal(err)
	}
	issue := &Issue{RepoId: repo.Id, Index: 1, Name: "broken", PosterId: user.Id, Content: "it broke"}
	if err := NewIssue(issue); err != nil {
		t.Fatal(err)
	}
	label := &Label{RepoId: repo.Id, Name: "bug", Color: "#ff0000"}
	if err := NewLabel(label); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateComment(user.Id, repo.Id, issue.Id, COMMENT_TYPE_COMMENT, "me too", nil); err != nil {
		t.Fatal(err)
	}

	reload()

	reloadedUser, err := GetUserByName("alice")
	if err != nil {
		t.Fatal(err)
	}
	reloadedIssue, err := GetIssueByIndex(repo.Id, 1)
	if err != nil {
		t.Fatal(err)
	}
	reloadedComments, err := GetIssueComments(reloadedIssue.Id)
	if err != nil {
		t.Fatal(err)
	}
	labels, err := GetLabels(repo.Id)
	if err != nil {
		t.Fatal(err)
	}
	if reloadedUser.Id != user.Id || reloadedUser.Email != "alice@example.com" {
		t.Fatalf("fresh database lost user: %+v", reloadedUser)
	}
	if reloadedIssue.RepoId != repo.Id || reloadedIssue.PosterId != user.Id || len(reloadedComments) != 1 {
		t.Fatal("fresh database lost issue ownership or discussion")
	}
	if reloadedComments[0].PosterId != user.Id || reloadedComments[0].Content != "me too" {
		t.Fatalf("comment not preserved: %+v", reloadedComments[0])
	}
	if reloadedIssue.NumComments != 1 {
		t.Fatalf("NumComments = %d, want 1", reloadedIssue.NumComments)
	}
	if len(labels) != 1 || labels[0].Name != "bug" {
		t.Fatalf("labels not preserved: %+v", labels)
	}
}

func TestPasswordVerification(t *testing.T) {
	reload := newTestDatabase(t)

	user := &User{Name: "bob", Email: "bob@example.com", Passwd: "correct horse"}
	if err := CreateUser(user); err != nil {
		t.Fatal(err)
	}
	reload()

	stored, err := GetUserByName("bob")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Passwd == "correct horse" {
		t.Fatal("password stored in plain text")
	}
	if !stored.ValidtePassword("correct horse") {
		t.Fatal("correct password rejected")
	}
	if stored.ValidtePassword("wrong") || stored.ValidtePassword("") {
		t.Fatal("wrong password accepted")
	}
}
