package main

import (
	"bytes"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestRun_Usage(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"tether-passhash"}, &out, &errBuf)
	if code != 2 {
		t.Fatalf("expected 2, got %d", code)
	}
	if !strings.Contains(errBuf.String(), "usage") {
		t.Fatalf("expected usage in stderr, got %q", errBuf.String())
	}
}

func TestRun_GeneratesValidBcryptHash(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"tether-passhash", "pw"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("expected 0, got %d stderr=%q", code, errBuf.String())
	}
	hash := strings.TrimSpace(out.String())
	if hash == "" {
		t.Fatalf("expected hash")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte("pw")); err != nil {
		t.Fatalf("hash does not match password: %v", err)
	}
}
