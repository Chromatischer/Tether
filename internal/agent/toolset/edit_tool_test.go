package toolset

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"tether/internal/userspace"
)

func editSessionWithFile(t *testing.T, content string) (*Session, string, string) {
	t.Helper()
	root := t.TempDir()
	rel := "workspace/file.txt"
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{Dirs: userspace.Dirs{Root: root}, ReadPaths: map[string]bool{}}
	return s, rel, p
}

func runEdit(t *testing.T, s *Session, args editFileArgs) (any, error) {
	t.Helper()
	raw, _ := json.Marshal(args)
	return EditFile{}.Execute(context.Background(), s, raw)
}

func TestEdit_RequiresPriorRead(t *testing.T) {
	s, rel, _ := editSessionWithFile(t, "hello world")
	if _, err := runEdit(t, s, editFileArgs{Path: rel, OldString: "hello", NewString: "hi"}); err == nil {
		t.Fatal("expected error when file was not read first")
	}
}

func TestEdit_UniqueReplacement(t *testing.T) {
	s, rel, p := editSessionWithFile(t, "port := 8080\n")
	s.MarkReadPath(rel)
	out, err := runEdit(t, s, editFileArgs{Path: rel, OldString: "8080", NewString: "9090"})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.(map[string]any); m["replacements"].(int) != 1 {
		t.Fatalf("replacements = %v", m["replacements"])
	}
	b, _ := os.ReadFile(p)
	if string(b) != "port := 9090\n" {
		t.Fatalf("content = %q", string(b))
	}
}

func TestEdit_NonUniqueRequiresReplaceAll(t *testing.T) {
	s, rel, p := editSessionWithFile(t, "a a a")
	s.MarkReadPath(rel)
	if _, err := runEdit(t, s, editFileArgs{Path: rel, OldString: "a", NewString: "b"}); err == nil {
		t.Fatal("expected error for non-unique match without replace_all")
	}
	out, err := runEdit(t, s, editFileArgs{Path: rel, OldString: "a", NewString: "b", ReplaceAll: true})
	if err != nil {
		t.Fatal(err)
	}
	if m := out.(map[string]any); m["replacements"].(int) != 3 {
		t.Fatalf("replacements = %v", m["replacements"])
	}
	b, _ := os.ReadFile(p)
	if string(b) != "b b b" {
		t.Fatalf("content = %q", string(b))
	}
}

func TestEdit_NotFound(t *testing.T) {
	s, rel, _ := editSessionWithFile(t, "hello")
	s.MarkReadPath(rel)
	if _, err := runEdit(t, s, editFileArgs{Path: rel, OldString: "nope", NewString: "x"}); err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestEdit_IdenticalStringsRejected(t *testing.T) {
	s, rel, _ := editSessionWithFile(t, "hello")
	s.MarkReadPath(rel)
	if _, err := runEdit(t, s, editFileArgs{Path: rel, OldString: "hello", NewString: "hello"}); err == nil {
		t.Fatal("expected error for identical old/new")
	}
}
