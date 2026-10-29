package models

import (
	"context"
	"testing"
)

func TestSpecialRepositoryName(t *testing.T) {
	newTestDatabase(t)
	root := newLifecycleRoot(t)
	runner := newFakeHgRunner(t)
	owner := newAccessTestUser(t, "profileowner", true)
	repo, err := CreateHgRepository(context.Background(), runner, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: ".github"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ChangeRepositoryName(repo.Id, "profile"); err != nil {
		t.Fatal(err)
	}
	if err := ChangeRepositoryName(repo.Id, ".github"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"..", ".", "../.hg", ".hg/store", `..\escape`} {
		if err := ChangeRepositoryName(repo.Id, name); err == nil {
			t.Errorf("rename accepted %q", name)
		}
		if _, err := CreateHgRepository(context.Background(), runner, root, CreateHgRepositoryOptions{OwnerID: owner.Id, Name: name}); err == nil {
			t.Errorf("create accepted %q", name)
		}
	}
}
