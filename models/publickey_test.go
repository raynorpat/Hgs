package models

import (
	"crypto/rand"
	"crypto/rsa"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestKeyStoreNeedsNoSSHExecutables(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	newTestDatabase(t)
	alice := newAccessTestUser(t, "alice", true)
	bob := newAccessTestUser(t, "bob", true)

	pub := newTestPublicKey(t)
	canonical := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub)))
	line := canonical + " alice@laptop\r\n"

	parsed, err := ParseKeyString(line)
	if err != nil || parsed != canonical {
		t.Fatalf("ParseKeyString = %q, %v; want %q", parsed, err, canonical)
	}

	key := &PublicKey{OwnerId: alice.Id, Name: "laptop", Content: line}
	if err = AddPublicKey(key); err != nil {
		t.Fatal(err)
	}
	keys, err := ListPublicKeys(alice.Id)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0].Name != "laptop" || keys[0].Content != canonical ||
		keys[0].Fingerprint != ssh.FingerprintSHA256(pub) {
		t.Fatalf("stored key: %+v", keys)
	}

	userID, keyID, err := AuthenticateRepositoryKey(pub)
	if err != nil || userID != alice.Id || keyID != key.Id {
		t.Fatalf("AuthenticateRepositoryKey = %d, %d, %v", userID, keyID, err)
	}

	// A fingerprint belongs to one account only.
	for _, dup := range []*PublicKey{
		{OwnerId: alice.Id, Name: "again", Content: canonical},
		{OwnerId: bob.Id, Name: "stolen", Content: canonical + " bob@desk"},
	} {
		if err = AddPublicKey(dup); err != ErrKeyAlreadyExist {
			t.Fatalf("duplicate key %q: %v", dup.Name, err)
		}
	}

	weak, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	weakPub, err := ssh.NewPublicKey(&weak.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	other := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(newTestPublicKey(t))))
	for name, content := range map[string]string{
		"options":       `command="sh",no-pty ` + canonical,
		"two keys":      canonical + "\n" + other,
		"garbage":       "ssh-ed25519 not-base64",
		"empty":         "  ",
		"weak RSA":      string(ssh.MarshalAuthorizedKey(weakPub)),
		"private block": "-----BEGIN OPENSSH PRIVATE KEY-----",
	} {
		if _, err = ParseKeyString(content); err == nil {
			t.Errorf("%s: ParseKeyString accepted %q", name, content)
		}
		if err = AddPublicKey(&PublicKey{OwnerId: bob.Id, Name: name, Content: content}); err == nil {
			t.Errorf("%s: AddPublicKey accepted %q", name, content)
		}
	}

	if _, _, err = AuthenticateRepositoryKey(newTestPublicKey(t)); err != ErrInvalidCredential {
		t.Fatalf("unknown key: %v", err)
	}

	// Another user cannot delete the key.
	if err = DeletePublicKey(&PublicKey{Id: key.Id, OwnerId: bob.Id}); err != ErrKeyNotExist {
		t.Fatalf("delete by non-owner: %v", err)
	}
	if err = DeletePublicKey(&PublicKey{Id: key.Id, OwnerId: alice.Id}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = AuthenticateRepositoryKey(pub); err != ErrInvalidCredential {
		t.Fatalf("deleted key authenticated: %v", err)
	}
	if err = AddPublicKey(&PublicKey{OwnerId: bob.Id, Name: "reused", Content: canonical}); err != nil {
		t.Fatalf("re-adding a deleted key: %v", err)
	}
}
