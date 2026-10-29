package models

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/setting"
)

// A token for one account must never confirm an address now owned by another.
func TestContributionEmailProof(t *testing.T) {
	newTestDatabase(t)
	oldLives := setting.Service.ActiveCodeLives
	setting.Service.ActiveCodeLives = 60
	t.Cleanup(func() { setting.Service.ActiveCodeLives = oldLives })
	original := &User{Name: "proof-original", LowerName: "proof-original", Email: "original@example.invalid", IsActive: true, Rands: "original-salt"}
	next := &User{Name: "proof-next", LowerName: "proof-next", Email: "next@example.invalid", IsActive: true}
	if _, err := x.Insert(original, next); err != nil {
		t.Fatal(err)
	}
	address := "reassigned@example.invalid"
	data := strconv.FormatInt(original.Id, 10) + address + original.LowerName + original.Passwd + original.Rands
	code := base.CreateTimeLimitCode(data, 60, nil) + hex.EncodeToString([]byte(original.LowerName))
	if _, err := x.Insert(&EmailAddress{Uid: next.Id, Email: address}); err != nil {
		t.Fatal(err)
	}
	if got := VerifyActiveEmailCode(code, address); got != nil {
		t.Fatalf("confirmation token for account %d accepted current owner %d", original.Id, got.Uid)
	}
	if _, err := ConsumeEmailVerification(context.Background(), code, address, false, time.Now()); !errors.Is(err, ErrEmailProofInvalid) {
		t.Fatalf("reassigned token consumed: %v", err)
	}
}

func TestContributionVerifiedAuthor(t *testing.T) {
	newTestDatabase(t)
	ctx := context.Background()
	u := &User{Name: "proof-owner", LowerName: "proof-owner", Email: "Alice+tag@Example.Invalid", IsActive: true, Rands: "salt"}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifiedContributionAuthor(ctx, " alice+tag@example.invalid "); err != nil || id != 0 {
		t.Fatalf("automatic activation became proof: %d %v", id, err)
	}
	now := time.Now()
	code, err := CreateEmailVerification(ctx, u.Id, u.Email, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = CreateEmailVerification(ctx, u.Id, u.Email, now.Add(179*time.Second)); !errors.Is(err, ErrEmailProofLimited) {
		t.Fatalf("resend: %v", err)
	}
	if _, err = ConsumeEmailVerification(ctx, code, "", false, now); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifiedContributionAuthor(ctx, " alice+tag@example.invalid "); err != nil || id != u.Id {
		t.Fatalf("confirmed attribution: %d %v", id, err)
	}
	if _, err = ConsumeEmailVerification(ctx, code, "", false, now); !errors.Is(err, ErrEmailProofInvalid) {
		t.Fatalf("replay accepted: %v", err)
	}
	other := &User{Name: "ambiguous", LowerName: "ambiguous", Email: "alice+tag@example.invalid", IsActive: true}
	if _, err = x.Insert(other); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifiedContributionAuthor(ctx, u.Email); err != nil || id != 0 {
		t.Fatalf("ambiguous owner attributed: %d %v", id, err)
	}
}

func TestContributionEmailProofExpiry(t *testing.T) {
	for _, offset := range []int{-7 * 3600, 8 * 3600} {
		newTestDatabase(t)
		u := &User{Name: "expiry", LowerName: "expiry", Email: "expiry@example.invalid", IsActive: true}
		if _, err := x.Insert(u); err != nil {
			t.Fatal(err)
		}
		now := time.Now().In(time.FixedZone("fixture", offset))
		code, err := CreateEmailVerification(context.Background(), u.Id, u.Email, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = ConsumeEmailVerification(context.Background(), code, "", false, now.Add(48*time.Hour)); !errors.Is(err, ErrEmailProofInvalid) {
			t.Fatalf("expired offset %d accepted: %v", offset, err)
		}
	}
}

func TestContributionEmailProofRemoval(t *testing.T) {
	newTestDatabase(t)
	ctx := context.Background()
	u := &User{Name: "remove-proof", LowerName: "remove-proof", Email: "old@example.invalid", IsActive: true}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	code, err := CreateEmailVerification(ctx, u.Id, u.Email, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ConsumeEmailVerification(ctx, code, "", false, now); err != nil {
		t.Fatal(err)
	}
	u.Email = "new@example.invalid"
	if err = UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	u.Email = "old@example.invalid"
	if err = UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifiedContributionAuthor(ctx, u.Email); err != nil || id != 0 {
		t.Fatalf("removed proof revived after readdition: %d %v", id, err)
	}
}

func TestContributionEmailProofLegacyAmbiguity(t *testing.T) {
	newTestDatabase(t)
	old := setting.Service.ActiveCodeLives
	setting.Service.ActiveCodeLives = 60
	t.Cleanup(func() { setting.Service.ActiveCodeLives = old })
	u := &User{Name: "legacy", LowerName: "legacy", Email: "Legacy@example.invalid", Rands: "legacy-salt", PendingActivation: true}
	v := &User{Name: "conflict", LowerName: "conflict", Email: "legacy@example.invalid", IsActive: true}
	if _, err := x.Insert(u, v); err != nil {
		t.Fatal(err)
	}
	data := strconv.FormatInt(u.Id, 10) + u.Email + u.LowerName + u.Passwd + u.Rands
	code := base.CreateTimeLimitCode(data, 60, nil) + hex.EncodeToString([]byte(u.LowerName))
	got, err := ConsumeEmailVerification(context.Background(), code, "", true, time.Now())
	if err != nil || got == nil || !got.IsActive {
		t.Fatalf("legitimate legacy activation denied by attribution ambiguity: %v", err)
	}
	if id, err := VerifiedContributionAuthor(context.Background(), u.Email); err != nil || id != 0 {
		t.Fatalf("ambiguous confirmation created proof: %d %v", id, err)
	}
}

func TestContributionEmailProofPurpose(t *testing.T) {
	newTestDatabase(t)
	u := &User{Name: "purpose", LowerName: "purpose", Email: "purpose@example.invalid", IsActive: true}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	code, err := CreateEmailVerification(context.Background(), u.Id, u.Email, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ConsumeEmailVerification(context.Background(), code, "", true, time.Now()); !errors.Is(err, ErrEmailProofInvalid) {
		t.Fatalf("ownership token accepted as account activation/sign-in: %v", err)
	}
}

func TestContributionEmailProofContinuousOwnership(t *testing.T) {
	newTestDatabase(t)
	u := &User{Name: "continuous", LowerName: "continuous", Email: "primary@example.invalid", IsActive: true}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	secondary := &EmailAddress{Uid: u.Id, Email: "secondary@example.invalid", IsActivated: true}
	if err := AddEmailAddress(secondary); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	now := time.Now()
	code, err := CreateEmailVerification(ctx, u.Id, secondary.Email, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ConsumeEmailVerification(ctx, code, "", false, now); err != nil {
		t.Fatal(err)
	}
	if err = MakeEmailPrimary(&EmailAddress{Id: secondary.Id}); err != nil {
		t.Fatal(err)
	}
	u, err = GetUserById(u.Id)
	if err != nil {
		t.Fatal(err)
	}
	u.FullName = "edited profile"
	u.AvatarEmail = "unrelated-avatar@example.invalid"
	if err = UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifiedContributionAuthor(ctx, secondary.Email); err != nil || id != u.Id {
		t.Fatalf("continuous ownership lost proof: %d %v", id, err)
	}
	if id, err := VerifiedContributionAuthor(ctx, u.AvatarEmail); err != nil || id != 0 {
		t.Fatalf("avatar preference became proof: %d %v", id, err)
	}
	if err = DeleteEmailAddress(&EmailAddress{Id: secondary.Id}); err != nil {
		t.Fatal(err)
	}
	if id, err := VerifiedContributionAuthor(ctx, secondary.Email); err != nil || id != u.Id {
		t.Fatalf("removing duplicate row invalidated still-owned primary: %d %v", id, err)
	}
}

func TestContributionEmailConfirmationAtomic(t *testing.T) {
	newTestDatabase(t)
	u := &User{Name: "atomic-confirm", LowerName: "atomic-confirm", Email: "atomic-primary@example.invalid", IsActive: true}
	if _, err := x.Insert(u); err != nil {
		t.Fatal(err)
	}
	ctx, now := context.Background(), time.Now()
	first := &EmailAddress{Uid: u.Id, Email: "atomic-first@example.invalid"}
	if err := AddEmailAddressWithConfirmation(ctx, first, now); err != nil {
		t.Fatal(err)
	}
	second := &EmailAddress{Uid: u.Id, Email: "atomic-second@example.invalid"}
	if err := AddEmailAddressWithConfirmation(ctx, second, now.Add(time.Second)); !errors.Is(err, ErrEmailProofLimited) {
		t.Fatalf("second delivery: %v", err)
	}
	if found, err := x.Where("email = ?", second.Email).Get(new(EmailAddress)); err != nil || found {
		t.Fatalf("throttled row persisted: %v %v", found, err)
	}
	if _, err := CreateEmailVerification(ctx, u.Id, u.Email, now.Add(time.Second)); !errors.Is(err, ErrEmailProofLimited) {
		t.Fatalf("explicit verification bypassed shared bound: %v", err)
	}
	if _, err := x.Exec("CREATE TRIGGER reject_confirmation_email BEFORE INSERT ON email_address BEGIN SELECT RAISE(ABORT, 'private failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := AddEmailAddressWithConfirmation(ctx, second, now.Add(181*time.Second)); err == nil {
		t.Fatal("insert failure succeeded")
	}
	if _, err := x.Exec("DROP TRIGGER reject_confirmation_email"); err != nil {
		t.Fatal(err)
	}
	if err := AddEmailAddressWithConfirmation(ctx, second, now.Add(181*time.Second)); err != nil {
		t.Fatalf("failed insert consumed delivery reservation: %v", err)
	}
}
