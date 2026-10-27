package hosting

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
	"testing"
)

func TestForkAdmissionBoundBeforeAllocation(t *testing.T) {
	s, owner, source := fixture(t)
	s.cfg.Mercurial.MaxProcesses = 1
	s.sessions.activeOperations["occupied-operation"] = true
	_, err := s.StartForkRepository(context.Background(), owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "bounded"})
	if !errors.Is(err, ErrMutationBusy) {
		t.Fatalf("bound did not reject before allocation: %v", err)
	}
	if _, err = models.GetRepositoryByName(owner.Id, "bounded"); err != models.ErrRepoNotExist {
		t.Fatalf("busy allocated repository: %v", err)
	}
}
