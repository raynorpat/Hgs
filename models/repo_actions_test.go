package models

import "testing"

func TestRepositoryWatchStarFailureRollback(t *testing.T) {
	for _, action := range []string{"watch", "unwatch", "star", "unstar"} {
		t.Run(action, func(t *testing.T) {
			newTestDatabase(t)
			u := &User{Name: "reader", LowerName: "reader", Email: "reader@example.invalid"}
			r := &Repository{Name: "project", LowerName: "project", State: RepositoryReady}
			if _, err := x.Insert(u, r); err != nil {
				t.Fatal(err)
			}
			watch := action == "watch" || action == "unwatch"
			remove := action == "unwatch" || action == "unstar"
			if remove {
				var err error
				if watch {
					err = WatchRepo(u.Id, r.Id, true)
				} else {
					err = StarRepo(u.Id, r.Id, true)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			trigger := `CREATE TRIGGER fail_counter BEFORE UPDATE OF num_watches ON repository BEGIN SELECT RAISE(FAIL, 'late watch failure'); END`
			if !watch {
				trigger = `CREATE TRIGGER fail_counter BEFORE UPDATE OF num_stars ON user BEGIN SELECT RAISE(FAIL, 'late star failure'); END`
			}
			if _, err := x.Exec(trigger); err != nil {
				t.Fatal(err)
			}
			var err error
			if watch {
				err = WatchRepo(u.Id, r.Id, !remove)
			} else {
				err = StarRepo(u.Id, r.Id, !remove)
			}
			if err == nil {
				t.Fatal("forced late failure was hidden")
			}
			if watch && IsWatching(u.Id, r.Id) != remove {
				t.Error("failed watch changed membership")
			}
			if !watch && IsStaring(u.Id, r.Id) != remove {
				t.Error("failed star changed membership")
			}
			gotRepo, err := GetRepositoryById(r.Id)
			if err != nil {
				t.Fatal(err)
			}
			gotUser, err := GetUserById(u.Id)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if remove {
				want = 1
			}
			if watch && gotRepo.NumWatches != want {
				t.Errorf("watch count=%d want %d", gotRepo.NumWatches, want)
			}
			if !watch && (gotRepo.NumStars != want || gotUser.NumStars != want) {
				t.Errorf("star counts repo/user=%d/%d want %d", gotRepo.NumStars, gotUser.NumStars, want)
			}
		})
	}
}
