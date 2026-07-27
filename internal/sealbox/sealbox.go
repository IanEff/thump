// Package sealbox is authenticated encryption for anything this engine puts
// in an object store: one AES-256-GCM seal per object, nonce prepended in
// the clear. Seal appends an authentication tag and Open verifies it before
// returning a byte, so a tampered object is an error, never plausible-looking
// JSON.
package sealbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
)

// Key is a 32-byte AES-256 key — THUMP_SEAL_KEY, base64-decoded.
type Key [32]byte

// Seal returns nonce||ciphertext||tag for plaintext, under a fresh 96-bit
// nonce read from crypto/rand on every call. Nonce reuse under the same key
// is catastrophic — it cancels the keystream between the two ciphertexts —
// so the nonce is never a counter and never persisted for reuse.
func (k Key) Seal(plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("sealbox: seal: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal, returning an error rather than plaintext for anything
// tampered with, truncated, or sealed under another key.
func (k Key) Open(sealed []byte) ([]byte, error) {
	gcm, err := newGCM(k)
	if err != nil {
		return nil, err
	}
	if len(sealed) < gcm.NonceSize() {
		return nil, fmt.Errorf("sealbox: open: sealed input shorter than a nonce")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("sealbox: open: %w", err)
	}
	return plaintext, nil
}

func newGCM(k Key) (cipher.AEAD, error) {
	block, err := aes.NewCipher(k[:])
	if err != nil {
		return nil, fmt.Errorf("sealbox: new cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("sealbox: new gcm: %w", err)
	}
	return gcm, nil
}
