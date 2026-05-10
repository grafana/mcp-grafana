package auth

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func mustKey(t *testing.T) []byte {
	t.Helper()
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestDecodeKey_Base64AndHex(t *testing.T) {
	raw := mustKey(t)
	b64 := base64.StdEncoding.EncodeToString(raw)
	hx := hex.EncodeToString(raw)

	for name, in := range map[string]string{"base64": b64, "hex": hx} {
		got, err := DecodeKey(in)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s: round-trip mismatch", name)
		}
	}
}

func TestDecodeKey_Errors(t *testing.T) {
	for _, in := range []string{"", "not-base64-or-hex!@#", "abcd"} {
		if _, err := DecodeKey(in); err == nil {
			t.Errorf("DecodeKey(%q) expected error", in)
		}
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	enc, err := NewEncryptor(mustKey(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := enc.Seal([]byte("super-secret"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := enc.Open(ct)
	if err != nil {
		t.Fatal(err)
	}
	if string(pt) != "super-secret" {
		t.Errorf("got %q want %q", pt, "super-secret")
	}
}

func TestOpen_TamperDetected(t *testing.T) {
	enc, err := NewEncryptor(mustKey(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := enc.Seal([]byte("data"))
	ct[len(ct)-1] ^= 0x01
	if _, err := enc.Open(ct); err == nil || !strings.Contains(err.Error(), "decrypt") {
		t.Errorf("tampered ciphertext should fail decrypt, got err=%v", err)
	}
}

func TestOpen_AcceptsPreviousKey(t *testing.T) {
	prev := mustKey(t)
	primary := mustKey(t)

	old, _ := mustEnc(t, prev, nil).Seal([]byte("rotated"))
	rotator, err := NewEncryptor(primary, prev)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := rotator.Open(old)
	if err != nil {
		t.Fatalf("should decrypt with previous key: %v", err)
	}
	if string(pt) != "rotated" {
		t.Errorf("got %q", pt)
	}

	// And new ciphertext is signed with the primary key only.
	fresh, _ := rotator.Seal([]byte("new"))
	if _, err := mustEnc(t, prev, nil).Open(fresh); err == nil {
		t.Errorf("previous-key-only Encryptor should NOT decrypt new ciphertext")
	}
}

func mustEnc(t *testing.T, primary, prev []byte) *Encryptor {
	t.Helper()
	e, err := NewEncryptor(primary, prev)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
