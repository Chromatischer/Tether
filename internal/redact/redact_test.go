package redact

import "testing"

func TestScanAndRedact_TokensAndPasswordLines(t *testing.T) {
	in := "hello ghp_abcdefghijklmnopqrstuvwxyz012345\npassword: supersecret\n"
	out, findings := ScanAndRedact(in)
	if out == in {
		t.Fatalf("expected redaction")
	}
	if len(findings) < 2 {
		t.Fatalf("expected findings, got %+v", findings)
	}
	if out != "hello [REDACTED]\npassword: [REDACTED]\n" {
		t.Fatalf("unexpected redacted output: %q", out)
	}
}

func TestScanAndRedact_NoFindings(t *testing.T) {
	in := "nothing to see here"
	out, findings := ScanAndRedact(in)
	if out != in {
		t.Fatalf("unexpected change: %q", out)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings")
	}
}
