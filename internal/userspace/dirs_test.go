package userspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForUserAndEnsure(t *testing.T) {
	root := t.TempDir()
	d := ForUser(root, 42)
	if d.Root == "" || d.Workspace == "" || d.Config == "" || d.Skills == "" || d.Cache == "" {
		t.Fatalf("expected all dirs to be set: %+v", d)
	}
	if filepath.Base(d.Root) != "42" {
		t.Fatalf("unexpected root path: %s", d.Root)
	}
	if err := Ensure(d); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{d.Root, d.Workspace, d.Config, d.Skills, d.Cache} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
		if !st.IsDir() {
			t.Fatalf("expected dir: %s", p)
		}
	}
}
