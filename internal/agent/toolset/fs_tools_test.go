package toolset

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tether/internal/userspace"
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

func TestWriteFile_PersonalityOverwrite_AfterRead_NoConfirm(t *testing.T) {
	root := t.TempDir()
	dirs := userspace.Dirs{Root: root}
	// Seed an existing personality file.
	p := filepath.Join(root, "config", "agents", "chat", "PERSONALITY.md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{UserID: 1, Dirs: dirs}
	readArgs, _ := json.Marshal(map[string]any{"path": "config/agents/chat/PERSONALITY.md"})
	if _, err := (ReadFile{}).Execute(nil, s, readArgs); err != nil {
		t.Fatalf("expected read ok, got: %v", err)
	}
	args, _ := json.Marshal(map[string]any{"path": "config/agents/chat/PERSONALITY.md", "content": "new"})
	_, err := (WriteFile{}).Execute(nil, s, args)
	if err != nil {
		t.Fatalf("expected ok, got: %v", err)
	}

	// Should have created a backup.
	histDir := filepath.Join(root, "config", "agents", "chat", ".history")
	ents, err := os.ReadDir(histDir)
	if err != nil {
		t.Fatalf("expected history dir: %v", err)
	}
	if len(ents) == 0 {
		t.Fatalf("expected at least one backup file")
	}
}

func TestWriteFile_OverwriteRequiresPriorRead(t *testing.T) {
	root := t.TempDir()
	dirs := userspace.Dirs{Root: root}
	p := filepath.Join(root, "workspace", "a.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{UserID: 1, Dirs: dirs}
	args, _ := json.Marshal(map[string]any{"path": "workspace/a.txt", "content": "new"})
	_, err := (WriteFile{}).Execute(nil, s, args)
	if err == nil || !strings.Contains(err.Error(), "requires reading it first") {
		t.Fatalf("expected prior-read error, got: %v", err)
	}
}

func TestWriteFile_OverwriteAfterReadAllowed(t *testing.T) {
	root := t.TempDir()
	dirs := userspace.Dirs{Root: root}
	p := filepath.Join(root, "workspace", "a.txt")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := &Session{UserID: 1, Dirs: dirs}
	readArgs, _ := json.Marshal(map[string]any{"path": "workspace/a.txt"})
	if _, err := (ReadFile{}).Execute(nil, s, readArgs); err != nil {
		t.Fatalf("expected read ok, got: %v", err)
	}

	writeArgs, _ := json.Marshal(map[string]any{"path": "workspace/a.txt", "content": "new"})
	if _, err := (WriteFile{}).Execute(nil, s, writeArgs); err != nil {
		t.Fatalf("expected write ok after read, got: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" {
		t.Fatalf("unexpected content: %q", string(b))
	}
}

func TestWriteFile_CreateNewFileWithoutRead(t *testing.T) {
	root := t.TempDir()
	dirs := userspace.Dirs{Root: root}
	s := &Session{UserID: 1, Dirs: dirs}

	args, _ := json.Marshal(map[string]any{"path": "workspace/new.txt", "content": "new"})
	if _, err := (WriteFile{}).Execute(nil, s, args); err != nil {
		t.Fatalf("expected create ok, got: %v", err)
	}
}
