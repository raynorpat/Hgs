// Copyright 2014 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package models

import (
	"crypto/dsa"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

var (
	ErrKeyAlreadyExist = errors.New("Public key already exists")
	ErrKeyNotExist     = errors.New("Public key does not exist")
)

// PublicKey represents a SSH key.
type PublicKey struct {
	Id                int64
	OwnerId           int64     `xorm:"UNIQUE(s) INDEX NOT NULL"`
	Name              string    `xorm:"UNIQUE(s) NOT NULL"`
	Fingerprint       string    `xorm:"UNIQUE NOT NULL"` // SHA256:<base64>
	Content           string    `xorm:"TEXT NOT NULL"`   // Canonical "<type> <base64>", no comment.
	Created           time.Time `xorm:"CREATED"`
	Updated           time.Time
	HasRecentActivity bool `xorm:"-"`
	HasUsed           bool `xorm:"-"`
}

// OmitEmail returns content of public key but without e-mail address.
func (k *PublicKey) OmitEmail() string {
	return strings.Join(strings.Split(k.Content, " ")[:2], " ")
}

// Minimum key sizes in bits; other key types have fixed safe sizes.
const (
	minRSAKeySize = 2048
	minDSAKeySize = 1024
)

func parsePublicKey(content string) (ssh.PublicKey, error) {
	key, _, options, rest, err := ssh.ParseAuthorizedKey([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("invalid public key: %v", err)
	} else if len(options) > 0 {
		return nil, errors.New("key options are not allowed")
	} else if len(strings.TrimSpace(string(rest))) > 0 {
		return nil, errors.New("only a single key please")
	}

	if ck, ok := key.(ssh.CryptoPublicKey); ok {
		switch k := ck.CryptoPublicKey().(type) {
		case *rsa.PublicKey:
			if k.N.BitLen() < minRSAKeySize {
				return nil, fmt.Errorf("the minimum accepted size of a RSA key is %d", minRSAKeySize)
			}
		case *dsa.PublicKey:
			if k.P.BitLen() < minDSAKeySize {
				return nil, fmt.Errorf("the minimum accepted size of a DSA key is %d", minDSAKeySize)
			}
		}
	}
	return key, nil
}

func canonicalKey(key ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
}

// ParseKeyString parses a single OpenSSH authorized_keys line without options
// and returns its canonical "<type> <base64>" form.
func ParseKeyString(content string) (string, error) {
	key, err := parsePublicKey(content)
	if err != nil {
		return "", err
	}
	return canonicalKey(key), nil
}

// AddPublicKey validates and adds new public key to database.
// A key fingerprint can belong to only one account.
func AddPublicKey(key *PublicKey) (err error) {
	pk, err := parsePublicKey(key.Content)
	if err != nil {
		return err
	}
	key.Content = canonicalKey(pk)
	key.Fingerprint = ssh.FingerprintSHA256(pk)

	if has, err := x.Get(&PublicKey{Fingerprint: key.Fingerprint}); err != nil {
		return err
	} else if has {
		return ErrKeyAlreadyExist
	}
	_, err = x.Insert(key)
	return err
}

// GetPublicKeyById returns public key by given ID.
func GetPublicKeyById(keyId int64) (*PublicKey, error) {
	key := new(PublicKey)
	has, err := x.Id(keyId).Get(key)
	if err != nil {
		return nil, err
	} else if !has {
		return nil, ErrKeyNotExist
	}
	return key, nil
}

// ListPublicKeys returns a list of public keys belongs to given user.
func ListPublicKeys(uid int64) ([]*PublicKey, error) {
	keys := make([]*PublicKey, 0, 5)
	err := x.Where("owner_id=?", uid).Find(&keys)
	if err != nil {
		return nil, err
	}

	for _, key := range keys {
		key.HasUsed = key.Updated.After(key.Created)
		key.HasRecentActivity = key.Updated.Add(7 * 24 * time.Hour).After(time.Now())
	}
	return keys, nil
}

// UpdatePublicKey updates given public key.
func UpdatePublicKey(key *PublicKey) error {
	_, err := x.Id(key.Id).AllCols().Update(key)
	return err
}

// DeletePublicKey deletes SSH key of given ID and owner.
func DeletePublicKey(key *PublicKey) error {
	if key.Id == 0 || key.OwnerId == 0 {
		return ErrKeyNotExist
	}
	n, err := x.Delete(&PublicKey{Id: key.Id, OwnerId: key.OwnerId})
	if err != nil {
		return err
	} else if n == 0 {
		return ErrKeyNotExist
	}
	return nil
}

// AuthenticateRepositoryKey returns the enabled account owning given key.
func AuthenticateRepositoryKey(key ssh.PublicKey) (userID, keyID int64, err error) {
	stored := &PublicKey{Fingerprint: ssh.FingerprintSHA256(key)}
	has, err := x.Get(stored)
	if err != nil {
		return 0, 0, err
	} else if !has || stored.Content != canonicalKey(key) {
		return 0, 0, ErrInvalidCredential
	}
	if _, err = enabledAccount(stored.OwnerId); err != nil {
		return 0, 0, err
	}
	return stored.OwnerId, stored.Id, nil
}
