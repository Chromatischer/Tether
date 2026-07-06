package toolset

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tether/internal/userspace"
)

func writePNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestViewImageAttaches(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "discord", "context", "x.png"))
	s := &Session{Dirs: userspace.Dirs{Root: root}, VisionEnabled: true}

	res, err := ViewImage{}.Execute(context.Background(), s, json.RawMessage(`{"path":"discord/context/x.png"}`))
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	m, ok := res.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T", res)
	}
	if m["type"] != "image/png" {
		t.Errorf("type=%v want image/png", m["type"])
	}
	if m["status"] != "attached" {
		t.Errorf("status=%v want attached", m["status"])
	}
	imgs := s.DrainPendingImages()
	if len(imgs) != 1 {
		t.Fatalf("expected 1 pending image, got %d", len(imgs))
	}
	if !strings.HasPrefix(imgs[0].DataURL, "data:image/png;base64,") {
		t.Errorf("data url prefix wrong: %.40q", imgs[0].DataURL)
	}
	if imgs[0].Path != "discord/context/x.png" {
		t.Errorf("path=%q", imgs[0].Path)
	}
	// Drain clears the queue.
	if got := s.DrainPendingImages(); got != nil {
		t.Errorf("expected drained queue, got %d", len(got))
	}
}

func TestViewImageRequiresVision(t *testing.T) {
	root := t.TempDir()
	writePNG(t, filepath.Join(root, "x.png"))
	s := &Session{Dirs: userspace.Dirs{Root: root}, VisionEnabled: false}
	if _, err := (ViewImage{}).Execute(context.Background(), s, json.RawMessage(`{"path":"x.png"}`)); err == nil {
		t.Fatal("expected error on non-vision model")
	}
}

func TestViewImageRejectsNonImage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("just text, definitely not an image"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &Session{Dirs: userspace.Dirs{Root: root}, VisionEnabled: true}
	if _, err := (ViewImage{}).Execute(context.Background(), s, json.RawMessage(`{"path":"notes.txt"}`)); err == nil {
		t.Fatal("expected error for non-image file")
	}
}

func TestViewImageRejectsEscape(t *testing.T) {
	root := t.TempDir()
	s := &Session{Dirs: userspace.Dirs{Root: root}, VisionEnabled: true}
	if _, err := (ViewImage{}).Execute(context.Background(), s, json.RawMessage(`{"path":"../etc/passwd"}`)); err == nil {
		t.Fatal("expected error for sandbox escape")
	}
}

func TestEnableViewImageGatedByVision(t *testing.T) {
	reg := toolsTestRegistry()
	s := NewSession(reg)
	if err := s.Enable("view_image"); err == nil {
		t.Fatal("expected Enable to refuse view_image without vision")
	}
	s.VisionEnabled = true
	if err := s.Enable("view_image"); err != nil {
		t.Fatalf("expected Enable to allow view_image with vision: %v", err)
	}
}
