package models

import "testing"

func TestIssueLabelCounterZerosPreserveIdentityAndEdits(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "labelowner", true)
	repository := newAccessTestRepo(t, owner, "labelrepo", false)
	label := &Label{RepoId: repository.Id, Name: "Keep label identity", Color: "#123456", NumIssues: 1, NumClosedIssues: 1}
	if err := NewLabel(label); err != nil {
		t.Fatal(err)
	}
	for _, count := range []int{0, 1, 0} {
		loaded, err := GetLabelById(label.Id)
		if err != nil {
			t.Fatal(err)
		}
		loaded.NumIssues = count
		loaded.NumClosedIssues = count
		if err := UpdateLabelIssueStats(loaded); err != nil {
			t.Fatal(err)
		}
		actual, err := GetLabelById(label.Id)
		if err != nil || actual.NumIssues != count || actual.NumClosedIssues != count || actual.RepoId != repository.Id || actual.Name != label.Name || actual.Color != label.Color {
			t.Fatalf("counter update changed/lost fields: %#v %v", actual, err)
		}
	}
	if err := UpdateLabelIssueStats(&Label{Id: label.Id, NumIssues: 1, NumClosedIssues: 1}); err != nil {
		t.Fatal(err)
	}
	if err := UpdateLabel(&Label{Id: label.Id, Name: "Edited name", Color: "#abcdef"}); err != nil {
		t.Fatal(err)
	}
	actual, err := GetLabelById(label.Id)
	if err != nil || actual.RepoId != repository.Id || actual.NumIssues != 1 || actual.NumClosedIssues != 1 || actual.Name != "Edited name" || actual.Color != "#abcdef" {
		t.Fatalf("partial edit damaged counters/identity: %#v %v", actual, err)
	}
}
