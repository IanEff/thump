package sealbox_test

import (
	"crypto/rand"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/ianeff/thump/internal/sealbox"
)

func TestKey_SealThenOpenRoundTripsThePlaintext(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		plaintext []byte
	}{
		"a normal plaintext round-trips": {[]byte("thump.decisions payload")},
		"an empty plaintext round-trips": {[]byte{}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			key := newTestKey(t)

			sealed, err := key.Seal(tc.plaintext)
			if err != nil {
				t.Fatal(err)
			}
			got, err := key.Open(sealed)
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(tc.plaintext, got, cmpopts.EquateEmpty()); diff != "" {
				t.Error("plaintext didn't round-trip", diff)
			}
		})
	}
}

func TestKey_OpenRejectsTamperedOrTruncatedInput(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	sealed, err := key.Seal([]byte("evidence digest"))
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string]struct {
		corrupt func([]byte) []byte
	}{
		"a flipped ciphertext byte is refused": {
			corrupt: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[len(out)-1] ^= 0xFF
				return out
			},
		},
		"a flipped nonce byte is refused": {
			corrupt: func(b []byte) []byte {
				out := append([]byte(nil), b...)
				out[0] ^= 0xFF
				return out
			},
		},
		"a truncated seal is refused": {
			corrupt: func(b []byte) []byte {
				return b[:len(b)-1]
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := key.Open(tc.corrupt(sealed)); err == nil {
				t.Error("Open of a corrupted seal returned nil error, want an error")
			}
		})
	}
}

func TestKey_OpenRejectsASealFromAnotherKey(t *testing.T) {
	t.Parallel()
	sealed, err := newTestKey(t).Seal([]byte("evidence digest"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newTestKey(t).Open(sealed); err == nil {
		t.Error("Open under a different key returned nil error, want an error")
	}
}

func TestKey_SealProducesDistinctBytesForIdenticalPlaintext(t *testing.T) {
	t.Parallel()
	key := newTestKey(t)
	plaintext := []byte("evidence digest")

	first, err := key.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	second, err := key.Seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(first, second); diff == "" {
		t.Error("two seals of identical plaintext produced identical bytes, want distinct nonces")
	}
}

func newTestKey(t *testing.T) sealbox.Key {
	t.Helper()
	var k sealbox.Key
	if _, err := rand.Read(k[:]); err != nil {
		t.Fatal(err)
	}
	return k
}
