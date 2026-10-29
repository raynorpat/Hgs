package hosting

import (
	"context"
	"errors"
	"github.com/gogits/gogs/models"
	"strings"
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

func TestForkMaintenanceAdmissionAttribution(t *testing.T) {
	s, owner, source := fixture(t)
	err := s.WithMaintenanceRead(context.Background(), source.Id, func(context.Context, string) error {
		_, err := s.StartForkRepository(context.Background(), owner.Id, source.Id, models.CreateHgRepositoryOptions{OwnerID: owner.Id, Name: "maintenance-busy"})
		if !errors.Is(err, ErrMutationBusy) || !strings.Contains(err.Error(), "maintenance-read") {
			t.Fatalf("unattributed rejecting lease: %v", err)
		}
		if _, err := models.GetRepositoryByName(owner.Id, "maintenance-busy"); !errors.Is(err, models.ErrRepoNotExist) {
			t.Fatalf("busy start allocated: %v", err)
		}
		if len(s.sessions.activeOperations) != 0 || len(s.sessions.reservations) != 0 {
			t.Fatal("busy start acquired mutation ownership")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
