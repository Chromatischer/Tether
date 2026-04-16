package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestSha256File(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256([]byte("abc"))
	exp := hex.EncodeToString(s[:])
	if got != exp {
		t.Fatalf("expected %s, got %s", exp, got)
	}
}

func TestCopyIfExists_CopiesRegularFileAndPreservesPerm(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	copied := copyIfExists(src, dst)
	if len(copied) != 1 || copied[0] != dst {
		t.Fatalf("unexpected copied list: %+v", copied)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "x" {
		t.Fatalf("unexpected content: %q", string(b))
	}
	st, _ := os.Stat(dst)
	if st.Mode().Perm() != 0o640 {
		t.Fatalf("expected perm 0640, got %v", st.Mode().Perm())
	}
}

func TestCopyIfExists_RejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	_ = os.WriteFile(target, []byte("secret"), 0o600)

	srcLink := filepath.Join(dir, "link")
	if err := os.Symlink(target, srcLink); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	dst := filepath.Join(dir, "dst")
	copied := copyIfExists(srcLink, dst)
	if len(copied) != 0 {
		t.Fatalf("expected symlink to be rejected")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatalf("expected dst to not exist")
	}
}

func TestCopyDirIfExists_SkipsDirs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	_ = os.MkdirAll(src, 0o755)
	_ = os.MkdirAll(filepath.Join(src, "subdir"), 0o755)
	_ = os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o600)

	copied := copyDirIfExists(src, dst)
	if len(copied) != 1 {
		t.Fatalf("expected one copied file, got %+v", copied)
	}
	if _, err := os.Stat(filepath.Join(dst, "a.txt")); err != nil {
		t.Fatalf("expected a.txt copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "subdir")); err == nil {
		t.Fatalf("did not expect subdir to be created")
	}
}
