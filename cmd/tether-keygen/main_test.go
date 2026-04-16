package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestRun_Generates32ByteKeyBase64(t *testing.T) {
	var out bytes.Buffer
	var errBuf bytes.Buffer
	code := run(&out, &errBuf)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d stderr=%q", code, errBuf.String())
	}
	b64 := strings.TrimSpace(out.String())
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("invalid base64: %v", err)
	}
	if len(b) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(b))
	}
}
