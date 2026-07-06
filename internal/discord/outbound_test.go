package discord

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tether/internal/config"
)

func TestParseOutboundAttachments(t *testing.T) {
	text := "Here is the report you asked for.\n\n[attachment name=\"report.pdf\" path=workspace/report.pdf]\n\nLet me know."
	cleaned, refs := parseOutboundAttachments(text)
	if len(refs) != 1 {
		t.Fatalf("expected 1 ref, got %d", len(refs))
	}
	if refs[0].Path != "workspace/report.pdf" || refs[0].Name != "report.pdf" {
		t.Errorf("ref parsed wrong: %+v", refs[0])
	}
	if strings.Contains(cleaned, "[attachment") {
		t.Errorf("notation not stripped: %q", cleaned)
	}
	if !strings.HasPrefix(cleaned, "Here is the report") || !strings.HasSuffix(cleaned, "Let me know.") {
		t.Errorf("cleaned text unexpected: %q", cleaned)
	}
}

func TestParseOutboundAttachmentsUnquotedAndMultiple(t *testing.T) {
	text := "files: [attachment path=a/b.txt] and [attachment#2 name=pic.png path=images/pic.png]"
	cleaned, refs := parseOutboundAttachments(text)
	if len(refs) != 2 {
		t.Fatalf("expected 2 refs, got %d (%+v)", len(refs), refs)
	}
	if refs[0].Path != "a/b.txt" || refs[0].Name != "" {
		t.Errorf("ref0 wrong: %+v", refs[0])
	}
	if refs[1].Path != "images/pic.png" || refs[1].Name != "pic.png" {
		t.Errorf("ref1 wrong: %+v", refs[1])
	}
	if strings.Contains(cleaned, "attachment") {
		t.Errorf("notation not stripped: %q", cleaned)
	}
}

func TestParseOutboundAttachmentsNone(t *testing.T) {
	text := "just a normal reply, no files."
	cleaned, refs := parseOutboundAttachments(text)
	if len(refs) != 0 {
		t.Fatalf("expected no refs, got %d", len(refs))
	}
	if cleaned != text {
		t.Errorf("text should be unchanged: %q", cleaned)
	}
}

func TestParseOutboundIgnoresErrorTag(t *testing.T) {
	// Inbound error notation has no path= and must not be treated as outbound.
	text := "[attachment error name=\"big.bin\" reason=\"too large\"]"
	cleaned, refs := parseOutboundAttachments(text)
	if len(refs) != 0 {
		t.Fatalf("expected no refs for error tag, got %d", len(refs))
	}
	if cleaned != text {
		t.Errorf("error tag should be preserved: %q", cleaned)
	}
}

func newOutboundGateway(t *testing.T) (*Gateway, string) {
	t.Helper()
	root := t.TempDir()
	cfg := &config.Config{}
	on := true
	cfg.Discord.Attachments.Enabled = &on
	cfg.Discord.Attachments.MaxSizeMB = 1
	cfg.Discord.Attachments.MaxPerMessage = 10
	return &Gateway{cfg: cfg}, root
}

func TestLoadOutboundFiles(t *testing.T) {
	g, root := newOutboundGateway(t)
	if err := os.MkdirAll(filepath.Join(root, "workspace"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workspace", "report.pdf"), []byte("PDFDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, notes := g.loadOutboundFiles(root, []outboundRef{{Path: "workspace/report.pdf", Name: "report.pdf"}})
	if len(notes) != 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
	if len(files) != 1 || files[0].Name != "report.pdf" {
		t.Fatalf("expected 1 file named report.pdf, got %+v", files)
	}
}

func TestLoadOutboundFilesMissingAndEscape(t *testing.T) {
	g, root := newOutboundGateway(t)
	files, notes := g.loadOutboundFiles(root, []outboundRef{
		{Path: "workspace/missing.txt"},
		{Path: "../../etc/passwd"},
	})
	if len(files) != 0 {
		t.Fatalf("expected no files, got %d", len(files))
	}
	if len(notes) != 2 {
		t.Fatalf("expected 2 error notes, got %d: %v", len(notes), notes)
	}
}

func TestLoadOutboundFilesOversize(t *testing.T) {
	g, root := newOutboundGateway(t)
	big := make([]byte, 2*1024*1024) // 2MB > 1MB cap
	if err := os.WriteFile(filepath.Join(root, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	files, notes := g.loadOutboundFiles(root, []outboundRef{{Path: "big.bin"}})
	if len(files) != 0 {
		t.Fatalf("expected oversize file rejected, got %d", len(files))
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "too large") {
		t.Fatalf("expected too-large note, got %v", notes)
	}
}

func TestResolveSandboxFileRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(target, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSandboxFile(root, "link.txt"); err == nil {
		t.Fatal("expected symlink to be rejected")
	}
}
