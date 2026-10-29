// Copyright 2026 The Gogs Authors. All rights reserved.
// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

package contributions

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"

	"github.com/gogits/gogs/modules/setting"
)

type cursor struct {
	Viewer, Owner              int64
	Organization               bool
	Year                       int
	Day, Kind                  string
	Cutoff                     int64
	Revision, Month, GroupKind string
	Repository                 int64
}

const cursorPurpose = "hgs-contribution-query-v1"

func cursorCipher() (cipher.AEAD, error) {
	if setting.SecretKey == "" {
		return nil, ErrStaleCursor
	}
	key := sha256.Sum256([]byte(cursorPurpose + "\x00" + setting.SecretKey))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func encodeCursor(c cursor) (string, error) {
	aead, err := cursorCipher()
	if err != nil {
		return "", err
	}
	plain, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(aead.Seal(nonce, nonce, plain, []byte(cursorPurpose))), nil
}
func decodeCursor(raw string) (cursor, error) {
	var c cursor
	if len(raw) > 4096 {
		return c, ErrStaleCursor
	}
	aead, err := cursorCipher()
	if err != nil {
		return c, ErrStaleCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(data) < aead.NonceSize()+aead.Overhead() {
		return c, ErrStaleCursor
	}
	plain, err := aead.Open(nil, data[:aead.NonceSize()], data[aead.NonceSize():], []byte(cursorPurpose))
	if err != nil || json.Unmarshal(plain, &c) != nil {
		return c, ErrStaleCursor
	}
	return c, nil
}
