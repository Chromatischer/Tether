package toolset

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUnderRoot_Basic(t *testing.T) {
	root := t.TempDir()
	p, err := resolveUnderRoot(root, "workspace/file.txt")
	if err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if filepath.Dir(p) == "/" {
		t.Fatalf("unexpected path: %s", p)
	}
}

func TestResolveUnderRoot_Escape(t *testing.T) {
	root := t.TempDir()
	_, err := resolveUnderRoot(root, "../etc/passwd")
	if err == nil {
		t.Fatalf("expected error")
	}
	_, err = resolveUnderRoot(root, "/etc/passwd")
	if err == nil {
		t.Fatalf("expected error")
	}
}

func TestResolveUnderRoot_SymlinkComponentRejected(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "dir", "etc")); err != nil {
		t.Fatal(err)
	}
	_, err := resolveUnderRoot(root, "dir/etc/passwd")
	if err == nil {
		t.Fatalf("expected error")
	}
}
