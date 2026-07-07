package main

// Password-based encryption for the RustDesk self-host credentials, so the repo
// can stay public: we commit only ciphertext (sealedRustDesk), and the installer
// decrypts it when the technician types the deployment password.
//
// Scheme: PBKDF2-HMAC-SHA256 (password → 256-bit key) then AES-256-GCM. The
// stored blob is base64(salt ‖ nonce ‖ ciphertext+tag). GCM authenticates, so a
// wrong password fails cleanly instead of yielding garbage.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

const (
	kdfIters = 600_000 // PBKDF2 iterations — deliberately slow to blunt brute force
	saltLen  = 16
	keyLen   = 32 // AES-256
)

// sealSecret encrypts plaintext with password and returns a base64 blob that is
// safe to commit to a public repo.
func sealSecret(plaintext, password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return "", err
	}
	gcm, err := gcmFor(password, salt)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nil, nonce, []byte(plaintext), nil)
	blob := append(append(append([]byte{}, salt...), nonce...), ct...)
	return base64.StdEncoding.EncodeToString(blob), nil
}

// openSecret reverses sealSecret. A wrong password returns an error.
func openSecret(blob, password string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return "", fmt.Errorf("malformed sealed blob: %w", err)
	}
	if len(raw) < saltLen {
		return "", fmt.Errorf("sealed blob too short")
	}
	salt, rest := raw[:saltLen], raw[saltLen:]
	gcm, err := gcmFor(password, salt)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(rest) < ns {
		return "", fmt.Errorf("sealed blob too short")
	}
	nonce, ct := rest[:ns], rest[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("wrong password or corrupted data")
	}
	return string(pt), nil
}

func gcmFor(password string, salt []byte) (cipher.AEAD, error) {
	key, err := pbkdf2.Key(sha256.New, password, salt, kdfIters, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
