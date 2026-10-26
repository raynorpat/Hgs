package models

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/gogits/gogs/modules/setting"
)

func newAccessTestUser(t *testing.T, name string, active bool) *User {
	t.Helper()
	u := &User{Name: name, Email: name + "@example.com", Passwd: name + "-password", IsActive: active}
	if err := CreateUser(u); err != nil {
		t.Fatal(err)
	}
	return u
}

func newAccessTestRepo(t *testing.T, owner *User, name string, private bool) *Repository {
	t.Helper()
	repo := &Repository{OwnerId: owner.Id, Name: name, LowerName: name, IsPrivate: private}
	if _, err := x.Insert(repo); err != nil {
		t.Fatal(err)
	}
	return repo
}

func newTestPublicKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	key, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func TestRepositoryAccessMatrix(t *testing.T) {
	newTestDatabase(t)
	oldRequire := setting.Service.RequireSignInView
	t.Cleanup(func() { setting.Service.RequireSignInView = oldRequire })
	setting.Service.RequireSignInView = false

	// The first user becomes the site administrator.
	siteAdmin := newAccessTestUser(t, "erin", true)
	if !siteAdmin.IsAdmin {
		t.Fatal("first user is not a site administrator")
	}
	owner := newAccessTestUser(t, "alice", true)
	reader := newAccessTestUser(t, "bob", true)
	writer := newAccessTestUser(t, "carol", true)
	stranger := newAccessTestUser(t, "dave", true)
	disabled := newAccessTestUser(t, "frank", false)

	private := newAccessTestRepo(t, owner, "secret", true)
	public := newAccessTestRepo(t, owner, "open", false)
	for _, a := range []*Access{
		{UserId: reader.Id, RepoId: private.Id, Mode: READABLE},
		{UserId: writer.Id, RepoId: private.Id, Mode: WRITABLE},
		{UserId: disabled.Id, RepoId: private.Id, Mode: WRITABLE},
		{UserId: disabled.Id, RepoId: public.Id, Mode: WRITABLE},
	} {
		if err := AddAccess(a); err != nil {
			t.Fatal(err)
		}
	}

	orgOwner := newAccessTestUser(t, "gina", true)
	teamReader := newAccessTestUser(t, "hank", true)
	teamAdmin := newAccessTestUser(t, "ivan", true)
	org, err := CreateOrganization(&User{Name: "acme", Email: "acme@example.com", Type: ORGANIZATION, IsActive: true}, orgOwner)
	if err != nil {
		t.Fatal(err)
	}
	orgRepo := newAccessTestRepo(t, org, "internal", true)
	ownerTeam, err := org.GetOwnerTeam()
	if err != nil {
		t.Fatal(err)
	}
	if err = ownerTeam.AddRepository(orgRepo); err != nil {
		t.Fatal(err)
	}
	readTeam := &Team{OrgId: org.Id, Name: "readers", Authorize: ORG_READABLE}
	adminTeam := &Team{OrgId: org.Id, Name: "admins", Authorize: ORG_ADMIN}
	for _, tm := range []struct {
		team   *Team
		member *User
	}{{readTeam, teamReader}, {adminTeam, teamAdmin}} {
		if err = NewTeam(tm.team); err != nil {
			t.Fatal(err)
		}
		if err = AddTeamMember(org.Id, tm.team.Id, tm.member.Id); err != nil {
			t.Fatal(err)
		}
		team, err := GetTeamById(tm.team.Id)
		if err != nil {
			t.Fatal(err)
		}
		if err = team.AddRepository(orgRepo); err != nil {
			t.Fatal(err)
		}
	}

	none, read, write, admin := RepositoryAccess{}, RepositoryAccess{Read: true},
		RepositoryAccess{Read: true, Write: true}, RepositoryAccess{Read: true, Write: true, Admin: true}
	cases := []struct {
		name   string
		userID int64
		repo   *Repository
		want   RepositoryAccess
	}{
		{"anonymous public", 0, public, read},
		{"anonymous private", 0, private, none},
		{"owner private", owner.Id, private, admin},
		{"owner public", owner.Id, public, admin},
		{"reader collaborator", reader.Id, private, read},
		{"writer collaborator", writer.Id, private, write},
		{"unrelated user private", stranger.Id, private, none},
		{"unrelated user public", stranger.Id, public, read},
		{"site administrator private", siteAdmin.Id, private, none},
		{"site administrator public", siteAdmin.Id, public, read},
		{"disabled collaborator private", disabled.Id, private, none},
		{"disabled collaborator public", disabled.Id, public, none},
		{"organization owner", orgOwner.Id, orgRepo, admin},
		{"read team member", teamReader.Id, orgRepo, read},
		{"admin team member", teamAdmin.Id, orgRepo, admin},
		{"user outside organization", stranger.Id, orgRepo, none},
		{"organization account itself", org.Id, orgRepo, none},
	}
	for _, c := range cases {
		got, err := RepositoryAccessFor(c.userID, c.repo.Id)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}

	setting.Service.RequireSignInView = true
	if got, err := RepositoryAccessFor(0, public.Id); err != nil || got != none {
		t.Fatalf("anonymous with sign-in required: %+v, %v", got, err)
	}
	if got, err := RepositoryAccessFor(stranger.Id, public.Id); err != nil || got != read {
		t.Fatalf("signed-in user with sign-in required: %+v, %v", got, err)
	}
	setting.Service.RequireSignInView = false

	if _, err = RepositoryAccessFor(owner.Id, 9999); err != ErrRepoNotExist {
		t.Fatalf("missing repository: %v", err)
	}
	if got, err := RepositoryAccessFor(9999, private.Id); err != nil || got != none {
		t.Fatalf("unknown user: %+v, %v", got, err)
	}

	// Revocation takes effect on the next check.
	if err = DeleteAccess(&Access{UserId: reader.Id, RepoId: private.Id}); err != nil {
		t.Fatal(err)
	}
	if got, err := RepositoryAccessFor(reader.Id, private.Id); err != nil || got != none {
		t.Fatalf("revoked collaborator: %+v, %v", got, err)
	}
	if err = RemoveTeamMember(org.Id, readTeam.Id, teamReader.Id); err != nil {
		t.Fatal(err)
	}
	if got, err := RepositoryAccessFor(teamReader.Id, orgRepo.Id); err != nil || got != none {
		t.Fatalf("removed team member: %+v, %v", got, err)
	}

	// Renames keep access rows; transfers rederive only ownership.
	private.Name = "renamed"
	if err = UpdateRepository(private); err != nil {
		t.Fatal(err)
	}
	if got, err := RepositoryAccessFor(writer.Id, private.Id); err != nil || got != write {
		t.Fatalf("writer after rename: %+v, %v", got, err)
	}
	if err = os.MkdirAll(RepoPath(owner.Name, private.Name), os.ModePerm); err != nil {
		t.Fatal(err)
	}
	private.Owner = owner
	if err = TransferOwnership(owner, stranger.Name, private); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		userID int64
		want   RepositoryAccess
	}{
		{"new owner", stranger.Id, admin},
		{"previous owner", owner.Id, none},
		{"collaborator after transfer", writer.Id, write},
	} {
		if got, err := RepositoryAccessFor(c.userID, private.Id); err != nil || got != c.want {
			t.Errorf("%s: got %+v, %v, want %+v", c.name, got, err, c.want)
		}
	}
}

func TestRepositoryAccessDatabaseOutage(t *testing.T) {
	newTestDatabase(t)
	owner := newAccessTestUser(t, "alice", true)
	public := newAccessTestRepo(t, owner, "open", false)
	key := newTestPublicKey(t)
	if err := x.Close(); err != nil {
		t.Fatal(err)
	}

	if got, err := RepositoryAccessFor(0, public.Id); err == nil || got.Read {
		t.Fatalf("anonymous read during outage: %+v, %v", got, err)
	}
	if got, err := RepositoryAccessFor(owner.Id, public.Id); err == nil || got.Read {
		t.Fatalf("owner read during outage: %+v, %v", got, err)
	}
	if _, _, err := AuthenticateRepositoryHTTPS("alice", "alice-password"); err == nil {
		t.Fatal("password authenticated during outage")
	}
	if _, _, err := AuthenticateRepositoryKey(key); err == nil {
		t.Fatal("key authenticated during outage")
	}
	if err := ValidateRepositoryCredential(owner.Id, 0, 0); err == nil {
		t.Fatal("credential validated during outage")
	}
}

func TestHTTPSCredentialRevocation(t *testing.T) {
	newTestDatabase(t)
	newAccessTestUser(t, "erin", true)
	owner := newAccessTestUser(t, "alice", true)
	bob := newAccessTestUser(t, "bob", true)
	carol := newAccessTestUser(t, "carol", true)
	repo := newAccessTestRepo(t, owner, "secret", true)
	if err := AddAccess(&Access{UserId: bob.Id, RepoId: repo.Id, Mode: READABLE}); err != nil {
		t.Fatal(err)
	}

	userID, tokenID, err := AuthenticateRepositoryHTTPS("bob", "bob-password")
	if err != nil || userID != bob.Id || tokenID != 0 {
		t.Fatalf("password auth: %d %d %v", userID, tokenID, err)
	}
	if _, _, err = AuthenticateRepositoryHTTPS("bob", "wrong"); err != ErrInvalidCredential {
		t.Fatalf("wrong password: %v", err)
	}
	if _, _, err = AuthenticateRepositoryHTTPS("", ""); err != ErrInvalidCredential {
		t.Fatalf("empty credential: %v", err)
	}

	token := &AccessToken{Uid: bob.Id, Name: "laptop"}
	if err = NewAccessToken(token); err != nil {
		t.Fatal(err)
	}
	userID, tokenID, err = AuthenticateRepositoryHTTPS(token.Sha1, "")
	if err != nil || userID != bob.Id || tokenID != token.Id {
		t.Fatalf("token auth: %d %d %v", userID, tokenID, err)
	}
	if err = ValidateRepositoryCredential(bob.Id, 0, token.Id); err != nil {
		t.Fatal(err)
	}
	if err = ValidateRepositoryCredential(carol.Id, 0, token.Id); err != ErrInvalidCredential {
		t.Fatalf("token of another user: %v", err)
	}

	key := &PublicKey{OwnerId: bob.Id, Name: "laptop", Content: string(ssh.MarshalAuthorizedKey(newTestPublicKey(t)))}
	if err = AddPublicKey(key); err != nil {
		t.Fatal(err)
	}
	if err = ValidateRepositoryCredential(bob.Id, key.Id, 0); err != nil {
		t.Fatal(err)
	}
	if err = ValidateRepositoryCredential(carol.Id, key.Id, 0); err != ErrInvalidCredential {
		t.Fatalf("key of another user: %v", err)
	}

	access, err := RepositoryAccessFor(userID, repo.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err = DeleteAccessTokenById(token.Id); err != nil {
		t.Fatal(err)
	}
	revokedCredentialErr := ValidateRepositoryCredential(bob.Id, 0, token.Id)
	if access.Read != true || access.Write != false || revokedCredentialErr == nil {
		t.Fatal("reader access or credential revocation is incorrect")
	}
	if _, _, err = AuthenticateRepositoryHTTPS(token.Sha1, ""); err != ErrInvalidCredential {
		t.Fatalf("revoked token authenticated: %v", err)
	}

	if err = DeletePublicKey(&PublicKey{Id: key.Id, OwnerId: bob.Id}); err != nil {
		t.Fatal(err)
	}
	if err = ValidateRepositoryCredential(bob.Id, key.Id, 0); err != ErrInvalidCredential {
		t.Fatalf("revoked key validated: %v", err)
	}

	// Disabling the account revokes password sessions and new logins.
	if err = ValidateRepositoryCredential(bob.Id, 0, 0); err != nil {
		t.Fatal(err)
	}
	bob.IsActive = false
	if _, err = x.Id(bob.Id).Cols("is_active").Update(bob); err != nil {
		t.Fatal(err)
	}
	if err = ValidateRepositoryCredential(bob.Id, 0, 0); err != ErrInvalidCredential {
		t.Fatalf("disabled account validated: %v", err)
	}
	if _, _, err = AuthenticateRepositoryHTTPS("bob", "bob-password"); err != ErrInvalidCredential {
		t.Fatalf("disabled account authenticated: %v", err)
	}
	if err = ValidateRepositoryCredential(0, 0, 0); err != ErrInvalidCredential {
		t.Fatalf("anonymous credential validated: %v", err)
	}
	if err = ValidateRepositoryCredential(9999, 0, 0); err != ErrInvalidCredential {
		t.Fatalf("unknown account validated: %v", err)
	}
}
