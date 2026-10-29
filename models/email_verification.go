// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/go-xorm/xorm"
	"github.com/gogits/gogs/modules/base"
	"github.com/gogits/gogs/modules/setting"
)

type EmailVerification struct {
	UserID          int64  `xorm:"index 'user_id'" json:"-"`
	NormalizedEmail string `xorm:"VARCHAR(254) unique" json:"-"`
	VerifiedAt      time.Time
	Revision        int64
}

type EmailVerificationChallenge struct {
	ID              string `xorm:"pk VARCHAR(64) 'id'" json:"-"`
	UserID          int64  `xorm:"index 'user_id'" json:"-"`
	NormalizedEmail string `xorm:"VARCHAR(254) index" json:"-"`
	ExpiresUnix     int64
	Consumed        bool
}

// Integer UTC instants avoid driver-specific datetime parameter comparisons.
type EmailVerificationDelivery struct {
	UserID   int64 `xorm:"pk 'user_id'"`
	SentUnix int64
}

// Serializes proof with account-email changes. It grants no account authority.
type EmailVerificationState struct {
	ID      int64 `xorm:"pk 'id'"`
	Version int64
}

var ErrEmailProofInvalid = errors.New("email verification: invalid or expired confirmation")
var ErrEmailProofLimited = errors.New("email verification: resend limited")

func NormalizeContributionEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func lockEmailVerification(s *xorm.Session) error {
	result, err := s.Exec("UPDATE email_verification_state SET version = version + 1 WHERE id = 1")
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n > 0 {
		return err
	}
	_, err = s.Insert(&EmailVerificationState{ID: 1, Version: 1})
	return err
}

func emailOwner(e Engine, email string) (int64, error) {
	email = NormalizeContributionEmail(email)
	if email == "" || len(email) > 254 {
		return 0, nil
	}
	var rows []struct{ Owner int64 }
	query := "SELECT DISTINCT owner FROM (SELECT id AS owner FROM `user` WHERE lower(trim(email)) = ? AND type = 0 UNION SELECT a.uid AS owner FROM email_address a INNER JOIN `user` u ON u.id = a.uid WHERE lower(trim(a.email)) = ? AND u.type = 0) email_owners LIMIT 2"
	err := e.Where("1 = 1").Sql(query, email, email).Find(&rows)
	if err != nil || len(rows) != 1 {
		return 0, err
	}
	return rows[0].Owner, nil
}

func VerifiedContributionAuthor(ctx context.Context, email string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	email = NormalizeContributionEmail(email)
	var result struct {
		UserID int64 `xorm:"'user_id'"`
	}
	query := "SELECT v.user_id FROM email_verification v INNER JOIN (SELECT MIN(owner) AS owner, COUNT(DISTINCT owner) AS owners FROM (SELECT id AS owner FROM `user` WHERE lower(trim(email)) = ? AND type = 0 UNION SELECT a.uid AS owner FROM email_address a INNER JOIN `user` u ON u.id = a.uid WHERE lower(trim(a.email)) = ? AND u.type = 0) email_owners) ownership ON ownership.owner = v.user_id WHERE v.normalized_email = ? AND ownership.owners = 1"
	_, err := contributionEngine().Sql(query, email, email, email).Get(&result)
	return result.UserID, contributionError(err)
}

func invalidateEmailVerification(e Engine, email string) error {
	email = NormalizeContributionEmail(email)
	owner, err := emailOwner(e, email)
	if err != nil {
		return err
	}
	if _, err = e.Where("normalized_email = ? AND user_id <> ?", email, owner).Delete(new(EmailVerification)); err != nil {
		return err
	}
	_, err = e.Where("normalized_email = ? AND user_id <> ?", email, owner).Delete(new(EmailVerificationChallenge))
	return err
}

func reserveVerificationDelivery(e Engine, uid int64, now time.Time, initial bool) error {
	var delivery EmailVerificationDelivery
	ok, err := e.Id(uid).Get(&delivery)
	if err != nil {
		return err
	}
	if ok && !initial && now.Unix() < delivery.SentUnix+180 {
		return ErrEmailProofLimited
	}
	delivery.UserID, delivery.SentUnix = uid, now.Unix()
	if !ok {
		_, err = e.Insert(&delivery)
	} else {
		_, err = e.Id(uid).Cols("sent_unix").Update(&delivery)
	}
	return err
}

// ReserveEmailConfirmationDelivery shares the bound with legacy confirmation
// resend. Initial registration is allowed once before any delivery exists.
func ReserveEmailConfirmationDelivery(ctx context.Context, uid int64, now time.Time) error {
	return contributionTransaction(ctx, func(s *xorm.Session) error {
		if err := lockEmailVerification(s); err != nil {
			return err
		}
		return reserveVerificationDelivery(s, uid, now, false)
	})
}

// ReservePendingActivation proves only a local pending signup. It grants no
// session or repository authority. Account security updates take the same lock.
func ReservePendingActivation(ctx context.Context, name, password string, now time.Time) (user *User, err error) {
	if len(name) > 254 || len(password) > 4096 || password == "" {
		return nil, ErrEmailProofInvalid
	}
	err = contributionTransaction(ctx, func(s *xorm.Session) error {
		if err := lockEmailVerification(s); err != nil {
			return err
		}
		var u User
		ok, err := s.Where("lower_name = ?", strings.ToLower(strings.TrimSpace(name))).Get(&u)
		if err != nil {
			return err
		}
		if !ok || u.IsActive || !u.PendingActivation || u.Type != INDIVIDUAL || (u.LoginType != NOTYPE && u.LoginType != PLAIN) {
			return ErrEmailProofInvalid
		}
		proof := User{Passwd: password, Salt: u.Salt}
		proof.EncodePasswd()
		if subtle.ConstantTimeCompare([]byte(proof.Passwd), []byte(u.Passwd)) != 1 {
			return ErrEmailProofInvalid
		}
		if err := reserveVerificationDelivery(s, u.Id, now, false); err != nil {
			return err
		}
		user = &u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}

func verificationData(u User, c EmailVerificationChallenge) string {
	return "email-ownership:" + c.ID + ":" + strconv.FormatInt(u.Id, 10) + ":" + c.NormalizedEmail + ":" + u.LowerName + ":" + u.Passwd + ":" + u.Rands
}

// CreateEmailVerification must only be called after the caller confirms mail is
// configured. Address selection is still checked against current ownership here.
func CreateEmailVerification(ctx context.Context, uid int64, email string, now time.Time) (code string, err error) {
	err = contributionTransaction(ctx, func(s *xorm.Session) error {
		if err := lockEmailVerification(s); err != nil {
			return err
		}
		var u User
		ok, err := s.Id(uid).Get(&u)
		if err != nil {
			return err
		}
		if !ok {
			return ErrEmailProofInvalid
		}
		email = NormalizeContributionEmail(email)
		owner, err := emailOwner(s, email)
		if err != nil {
			return err
		}
		if owner != uid {
			return ErrEmailProofInvalid
		}
		if err = reserveVerificationDelivery(s, uid, now, false); err != nil {
			return err
		}
		if _, err = s.Where("user_id = ? AND (normalized_email = ? OR expires_unix <= ? OR consumed = ?)", uid, email, now.Unix(), true).Delete(new(EmailVerificationChallenge)); err != nil {
			return err
		}
		raw := make([]byte, 32)
		if _, err = rand.Read(raw); err != nil {
			return err
		}
		minutes := setting.Service.ActiveCodeLives
		if minutes <= 0 {
			minutes = 180
		}
		challenge := EmailVerificationChallenge{ID: hex.EncodeToString(raw), UserID: uid, NormalizedEmail: email, ExpiresUnix: now.Add(time.Duration(minutes) * time.Minute).Unix()}
		if _, err = s.Insert(&challenge); err != nil {
			return err
		}
		code = "v1." + base.CreateTimeLimitCode(verificationData(u, challenge), minutes, base.DateFormat(now, "YmdHi")) + "." + challenge.ID
		return nil
	})
	return
}

// ConsumeEmailVerification atomically binds a valid token to current ownership,
// persists proof and consumes the challenge/salt. Legacy activation eligibility
// is changed only for the legacy account/additional-email confirmation paths.
func ConsumeEmailVerification(ctx context.Context, code, email string, activateAccount bool, now time.Time) (user *User, err error) {
	if len(code) > 1024 {
		return nil, ErrEmailProofInvalid
	}
	err = contributionTransaction(ctx, func(s *xorm.Session) error {
		if err := lockEmailVerification(s); err != nil {
			return err
		}
		var u User
		explicit := strings.HasPrefix(code, "v1.")
		if explicit && activateAccount {
			return ErrEmailProofInvalid
		}
		if explicit {
			parts := strings.Split(code, ".")
			if len(parts) != 3 || len(parts[1]) != base.TimeLimitCodeLength || len(parts[2]) != 64 {
				return ErrEmailProofInvalid
			}
			var challenge EmailVerificationChallenge
			ok, err := s.Id(parts[2]).Get(&challenge)
			if err != nil {
				return err
			}
			if !ok || challenge.Consumed || now.Unix() >= challenge.ExpiresUnix {
				return ErrEmailProofInvalid
			}
			ok, err = s.Id(challenge.UserID).Get(&u)
			if err != nil {
				return err
			}
			if !ok {
				return ErrEmailProofInvalid
			}
			if !base.VerifyTimeLimitCode(verificationData(u, challenge), setting.Service.ActiveCodeLives, parts[1]) {
				return ErrEmailProofInvalid
			}
			email = challenge.NormalizedEmail
			if _, err = s.Id(challenge.ID).Cols("consumed").Update(&EmailVerificationChallenge{Consumed: true}); err != nil {
				return err
			}
		} else {
			if len(code) <= base.TimeLimitCodeLength {
				return ErrEmailProofInvalid
			}
			name, err := hex.DecodeString(code[base.TimeLimitCodeLength:])
			if err != nil {
				return ErrEmailProofInvalid
			}
			ok, err := s.Where("lower_name = ?", string(name)).Get(&u)
			if err != nil {
				return err
			}
			if !ok {
				return ErrEmailProofInvalid
			}
			if activateAccount {
				if u.IsActive || !u.PendingActivation || u.Type != INDIVIDUAL || (u.LoginType != NOTYPE && u.LoginType != PLAIN) {
					return ErrEmailProofInvalid
				}
				email = u.Email
			}
			data := strconv.FormatInt(u.Id, 10) + email + u.LowerName + u.Passwd + u.Rands
			if !base.VerifyTimeLimitCode(data, setting.Service.ActiveCodeLives, code[:base.TimeLimitCodeLength]) {
				return ErrEmailProofInvalid
			}
		}
		owner, err := emailOwner(s, email)
		if err != nil {
			return err
		}
		if explicit && owner != u.Id {
			return ErrEmailProofInvalid
		}
		normalized := NormalizeContributionEmail(email)
		if !explicit && !activateAccount {
			var additional EmailAddress
			ok, err := s.Where("uid = ? AND email = ?", u.Id, email).Get(&additional)
			if err != nil {
				return err
			}
			if !ok {
				return ErrEmailProofInvalid
			}
			if _, err = s.Id(additional.Id).Cols("is_activated").Update(&EmailAddress{IsActivated: true}); err != nil {
				return err
			}
		}
		if _, err = s.Where("normalized_email = ?", normalized).Delete(new(EmailVerification)); err != nil {
			return err
		}
		if owner == u.Id {
			revision, err := nextContributionRevision(s)
			if err != nil {
				return err
			}
			if _, err = s.Insert(&EmailVerification{UserID: u.Id, NormalizedEmail: normalized, VerifiedAt: now.UTC(), Revision: revision}); err != nil {
				return err
			}
		}
		if !explicit {
			u.Rands = GetUserSalt()
			cols := []string{"rands"}
			if activateAccount {
				u.IsActive = true
				u.PendingActivation = false
				cols = append(cols, "is_active", "pending_activation")
			}
			if _, err = s.Id(u.Id).Cols(cols...).Update(&u); err != nil {
				return err
			}
		}
		user = &u
		return nil
	})
	if err != nil {
		return nil, err
	}
	return user, nil
}
