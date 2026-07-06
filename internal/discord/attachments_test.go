package discord

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"tether/internal/config"
)

func newTestGateway(t *testing.T, body string) (*Gateway, string) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{}
	on := true
	cfg.Discord.Attachments.Enabled = &on
	cfg.Discord.Attachments.MaxSizeMB = 1
	cfg.Discord.Attachments.MaxPerMessage = 10
	cfg.Discord.Attachments.RetentionDays = 14
	g := &Gateway{
		cfg: cfg,
		fetch: func(_ context.Context, _ string, w io.Writer, limit int64) (int64, error) {
			return io.Copy(w, io.LimitReader(strings.NewReader(body), limit))
		},
	}
	return g, root
}

func TestIngestAttachmentsSavesAndClassifies(t *testing.T) {
	g, root := newTestGateway(t, "hello bytes")
	atts := []*discordgo.MessageAttachment{
		{ID: "1", URL: "http://cdn/x", Filename: "photo.jpg", ContentType: "image/jpeg", Size: 11},
		{ID: "2", URL: "http://cdn/y", Filename: "voice-message.ogg", ContentType: "audio/ogg", Size: 11, DurationSecs: 7},
		{ID: "3", URL: "http://cdn/z", Filename: "notes.pdf", ContentType: "application/pdf", Size: 11},
	}
	saved, errs := g.ingestAttachments(context.Background(), root, atts)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %+v", errs)
	}
	if len(saved) != 3 {
		t.Fatalf("expected 3 saved, got %d", len(saved))
	}
	wantKinds := []attachmentKind{attachmentImage, attachmentVoice, attachmentFile}
	for i, a := range saved {
		if a.Kind != wantKinds[i] {
			t.Errorf("attachment %d: kind=%q want %q", i, a.Kind, wantKinds[i])
		}
		if !strings.HasPrefix(a.RelPath, "discord/context/") {
			t.Errorf("attachment %d: relpath=%q not under discord/context", i, a.RelPath)
		}
		abs := filepath.Join(root, filepath.FromSlash(a.RelPath))
		b, err := os.ReadFile(abs)
		if err != nil {
			t.Errorf("attachment %d: file not written: %v", i, err)
			continue
		}
		if string(b) != "hello bytes" {
			t.Errorf("attachment %d: content=%q", i, string(b))
		}
	}
	// Extensions preserved.
	if filepath.Ext(saved[0].RelPath) != ".jpg" {
		t.Errorf("expected .jpg ext, got %q", saved[0].RelPath)
	}
}

func TestIngestAttachmentsRejectsOversize(t *testing.T) {
	g, root := newTestGateway(t, "x")
	atts := []*discordgo.MessageAttachment{
		{ID: "1", URL: "http://cdn/x", Filename: "big.bin", ContentType: "application/octet-stream", Size: 5 << 20},
	}
	saved, errs := g.ingestAttachments(context.Background(), root, atts)
	if len(saved) != 0 {
		t.Fatalf("expected nothing saved, got %d", len(saved))
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Reason, "too large") {
		t.Fatalf("expected too-large error, got %+v", errs)
	}
}

func TestIngestAttachmentsRejectsOversizeDownload(t *testing.T) {
	// Declared size is small but the body exceeds the cap; download must be rejected.
	body := strings.Repeat("a", 2<<20)
	g, root := newTestGateway(t, body)
	atts := []*discordgo.MessageAttachment{
		{ID: "1", URL: "http://cdn/x", Filename: "sneaky.bin", ContentType: "application/octet-stream", Size: 10},
	}
	saved, errs := g.ingestAttachments(context.Background(), root, atts)
	if len(saved) != 0 {
		t.Fatalf("expected nothing saved, got %d", len(saved))
	}
	if len(errs) != 1 || !strings.Contains(errs[0].Reason, "size limit") {
		t.Fatalf("expected size-limit error, got %+v", errs)
	}
	// No partial file left behind.
	files, _ := os.ReadDir(filepath.Join(root, "discord", "context"))
	if len(files) != 0 {
		t.Fatalf("expected no leftover files, got %d", len(files))
	}
}

func TestIngestAttachmentsDisabled(t *testing.T) {
	g, root := newTestGateway(t, "x")
	off := false
	g.cfg.Discord.Attachments.Enabled = &off
	saved, errs := g.ingestAttachments(context.Background(), root, []*discordgo.MessageAttachment{{Filename: "a.txt", Size: 1}})
	if saved != nil || errs != nil {
		t.Fatalf("expected no-op when disabled, got %+v / %+v", saved, errs)
	}
}

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"photo.jpg":         "photo.jpg",
		"../../etc/passwd":  "passwd",
		"weird name!@#.png": "weird_name___.png",
		"":                  "file",
		"..":                "file",
		"/abs/path/x.bin":   "x.bin",
	}
	for in, want := range cases {
		if got := sanitizeFilename(in); got != want {
			t.Errorf("sanitizeFilename(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPickExtension(t *testing.T) {
	if ext := pickExtension("photo.JPG", "image/jpeg"); ext != ".jpg" {
		t.Errorf("filename ext: got %q", ext)
	}
	if ext := pickExtension("noext", "application/pdf"); ext != ".pdf" {
		t.Errorf("content-type ext: got %q", ext)
	}
	if ext := pickExtension("weird.verylongextension", "application/unknown-xyz"); ext != "" {
		t.Errorf("expected empty ext, got %q", ext)
	}
}

func TestRenderAttachmentBlock(t *testing.T) {
	saved := []savedAttachment{
		{Kind: attachmentImage, RelPath: "discord/context/ab12.jpg", Filename: "p.jpg", ContentType: "image/jpeg", Size: 2048},
		{Kind: attachmentVoice, RelPath: "discord/context/cd34.ogg", Filename: "voice-message.ogg", ContentType: "audio/ogg", Size: 4096, DurationSecs: 7},
	}
	errs := []attachmentError{{Filename: "big.bin", Reason: "too large"}}
	out := renderAttachmentBlock(saved, errs)
	for _, want := range []string{
		"[attachment#1 image", "path=discord/context/ab12.jpg",
		"[attachment#2 voice", "duration=7s", "path=discord/context/cd34.ogg",
		"[attachment error", "too large",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("block missing %q in:\n%s", want, out)
		}
	}
}

func TestCombineMessageText(t *testing.T) {
	block := "[attachment#1 image path=discord/context/x.jpg]"
	// Attachment-only: just the notation block.
	if got := combineMessageText("", block); got != block {
		t.Errorf("attachment-only combine wrong: %q", got)
	}
	// With text.
	got := combineMessageText("look at this", block)
	if !strings.HasPrefix(got, "look at this") || !strings.Contains(got, block) {
		t.Errorf("text+attachment combine wrong: %q", got)
	}
	// No attachments.
	if got := combineMessageText("just text", ""); got != "just text" {
		t.Errorf("no-attachment combine wrong: %q", got)
	}
}

func TestPruneAttachments(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.DataDir = root
	cfg.Discord.Attachments.RetentionDays = 14
	g := &Gateway{cfg: cfg}

	dir := filepath.Join(root, "users", "42", "discord", "context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldFile := filepath.Join(dir, "old.bin")
	newFile := filepath.Join(dir, "new.bin")
	if err := os.WriteFile(oldFile, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newFile, []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, old, old); err != nil {
		t.Fatal(err)
	}

	g.pruneAttachments()

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Errorf("expected old file removed, err=%v", err)
	}
	if _, err := os.Stat(newFile); err != nil {
		t.Errorf("expected new file kept, err=%v", err)
	}
}
