package discord

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"golang.org/x/sys/unix"
)

// attachmentKind classifies an inbound Discord attachment so the agent can
// decide how to handle it.
type attachmentKind string

const (
	attachmentImage attachmentKind = "image"
	attachmentVoice attachmentKind = "voice"
	attachmentAudio attachmentKind = "audio"
	attachmentFile  attachmentKind = "file"
)

// savedAttachment describes a downloaded attachment persisted under the user
// sandbox. RelPath is relative to the sandbox root, so the agent can read it
// directly with the `read` tool.
type savedAttachment struct {
	Kind         attachmentKind
	RelPath      string
	Filename     string
	ContentType  string
	Size         int
	DurationSecs float64
	SHA256       string
}

// attachmentError records why a single attachment could not be ingested.
type attachmentError struct {
	Filename string
	Reason   string
}

// attachmentFetcher downloads url into w, copying at most limit bytes. It is a
// field on the Gateway so tests can substitute an httptest-backed fetcher.
type attachmentFetcher func(ctx context.Context, url string, w io.Writer, limit int64) (int64, error)

// httpFetch is the production fetcher. Discord CDN URLs are trusted, so the
// SSRF guard used for arbitrary user-supplied URLs is intentionally not applied
// here; the download is bounded by an io.LimitReader.
func httpFetch(ctx context.Context, url string, w io.Writer, limit int64) (int64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download failed: %s", resp.Status)
	}
	return io.Copy(w, io.LimitReader(resp.Body, limit))
}

// ingestAttachments downloads each attachment into <root>/discord/context and
// returns the saved descriptors plus per-file errors. It never returns an error
// for individual failures; those are surfaced via attachmentError so the agent
// (and user) can see what was dropped.
func (g *Gateway) ingestAttachments(ctx context.Context, root string, atts []*discordgo.MessageAttachment) ([]savedAttachment, []attachmentError) {
	ac := g.cfg.Discord.Attachments
	if ac.Enabled == nil || !*ac.Enabled || len(atts) == 0 {
		return nil, nil
	}

	maxBytes := int64(ac.MaxSizeMB) * 1024 * 1024
	dir := filepath.Join(root, "discord", "context")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, []attachmentError{{Reason: "could not create context dir: " + err.Error()}}
	}

	var (
		saved []savedAttachment
		errs  []attachmentError
	)
	for i, att := range atts {
		if att == nil {
			continue
		}
		name := sanitizeFilename(att.Filename)
		if ac.MaxPerMessage > 0 && i >= ac.MaxPerMessage {
			errs = append(errs, attachmentError{Filename: name, Reason: "exceeded max attachments per message"})
			continue
		}
		if maxBytes > 0 && int64(att.Size) > maxBytes {
			errs = append(errs, attachmentError{Filename: name, Reason: fmt.Sprintf("too large (%d bytes, limit %d)", att.Size, maxBytes)})
			continue
		}

		id, err := randomID()
		if err != nil {
			errs = append(errs, attachmentError{Filename: name, Reason: "id generation failed"})
			continue
		}
		ext := pickExtension(name, att.ContentType)
		abs := filepath.Join(dir, id+ext)
		relPath := filepath.ToSlash(filepath.Join("discord", "context", id+ext))

		sum, n, err := downloadToFile(ctx, g.fetch, att.URL, abs, maxBytes)
		if err != nil {
			_ = os.Remove(abs)
			errs = append(errs, attachmentError{Filename: name, Reason: err.Error()})
			continue
		}

		saved = append(saved, savedAttachment{
			Kind:         classifyAttachment(att, name),
			RelPath:      relPath,
			Filename:     name,
			ContentType:  att.ContentType,
			Size:         int(n),
			DurationSecs: att.DurationSecs,
			SHA256:       sum,
		})
	}
	return saved, errs
}

// downloadToFile streams url into abs (with O_NOFOLLOW) while computing its
// SHA-256. It rejects downloads that exceed maxBytes.
func downloadToFile(ctx context.Context, fetch attachmentFetcher, url, abs string, maxBytes int64) (string, int64, error) {
	fd, err := unix.Open(abs, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NOFOLLOW, 0o644)
	if err != nil {
		return "", 0, err
	}
	f := os.NewFile(uintptr(fd), abs)
	defer f.Close()

	limit := int64(1) << 62
	if maxBytes > 0 {
		limit = maxBytes + 1
	}
	h := sha256.New()
	n, err := fetch(ctx, url, io.MultiWriter(f, h), limit)
	if err != nil {
		return "", n, err
	}
	if maxBytes > 0 && n > maxBytes {
		return "", n, fmt.Errorf("download exceeded size limit")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// classifyAttachment maps a Discord attachment to a coarse kind. Discord voice
// notes arrive as audio attachments with a non-zero duration and the canonical
// filename voice-message.ogg.
func classifyAttachment(att *discordgo.MessageAttachment, name string) attachmentKind {
	ct := strings.ToLower(strings.TrimSpace(att.ContentType))
	switch {
	case strings.HasPrefix(ct, "image/"):
		return attachmentImage
	case att.DurationSecs > 0 && (strings.HasPrefix(ct, "audio/") || strings.EqualFold(name, "voice-message.ogg")):
		return attachmentVoice
	case strings.EqualFold(name, "voice-message.ogg"):
		return attachmentVoice
	case strings.HasPrefix(ct, "audio/"):
		return attachmentAudio
	default:
		return attachmentFile
	}
}

// sanitizeFilename strips path separators and unusual characters from a
// user-supplied filename. The result is only used for display and extension
// inference; the on-disk name is always a generated identifier.
func sanitizeFilename(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.' || r == '-' || r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), ".")
	if out == "" {
		return "file"
	}
	if len(out) > 128 {
		out = out[len(out)-128:]
	}
	return out
}

// pickExtension chooses a safe extension from the filename, falling back to one
// derived from the declared content type.
func pickExtension(name, contentType string) string {
	if ext := filepath.Ext(name); isSafeExt(ext) {
		return strings.ToLower(ext)
	}
	if ct := strings.TrimSpace(contentType); ct != "" {
		if i := strings.IndexByte(ct, ';'); i >= 0 {
			ct = ct[:i]
		}
		if exts, err := mime.ExtensionsByType(strings.TrimSpace(ct)); err == nil && len(exts) > 0 {
			if isSafeExt(exts[0]) {
				return strings.ToLower(exts[0])
			}
		}
	}
	return ""
}

func isSafeExt(ext string) bool {
	if ext == "" || ext == "." {
		return false
	}
	if !strings.HasPrefix(ext, ".") {
		return false
	}
	for _, r := range ext[1:] {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		default:
			return false
		}
	}
	return len(ext) <= 12
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// renderAttachmentBlock formats saved attachments and errors as inline notation
// appended to the user's prompt text.
func renderAttachmentBlock(saved []savedAttachment, errs []attachmentError) string {
	var b strings.Builder
	for i, a := range saved {
		ct := a.ContentType
		if strings.TrimSpace(ct) == "" {
			ct = "application/octet-stream"
		}
		fmt.Fprintf(&b, "[attachment#%d %s name=%q type=%s size=%s", i+1, a.Kind, a.Filename, ct, humanSize(a.Size))
		if a.DurationSecs > 0 {
			fmt.Fprintf(&b, " duration=%ds", int(a.DurationSecs+0.5))
		}
		fmt.Fprintf(&b, " path=%s]\n", a.RelPath)
	}
	for _, e := range errs {
		name := e.Filename
		if strings.TrimSpace(name) == "" {
			name = "(unknown)"
		}
		fmt.Fprintf(&b, "[attachment error name=%q reason=%q]\n", name, e.Reason)
	}
	return strings.TrimRight(b.String(), "\n")
}

// combineMessageText merges the user's typed text with the attachment notation
// block into a single turn for the agent.
func combineMessageText(userText, attachBlock string) string {
	userText = strings.TrimSpace(userText)
	attachBlock = strings.TrimSpace(attachBlock)
	if attachBlock == "" {
		return userText
	}
	if userText == "" {
		return attachBlock
	}
	return userText + "\n\n" + attachBlock
}

func humanSize(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

// pruneAttachments deletes context files older than the configured retention
// across all users. Best-effort; errors are ignored.
func (g *Gateway) pruneAttachments() {
	days := g.cfg.Discord.Attachments.RetentionDays
	if days <= 0 {
		return
	}
	cutoff := time.Now().Add(-time.Duration(days) * 24 * time.Hour)
	usersDir := filepath.Join(g.cfg.Paths.DataDir, "users")
	users, err := os.ReadDir(usersDir)
	if err != nil {
		return
	}
	for _, u := range users {
		if !u.IsDir() {
			continue
		}
		dir := filepath.Join(usersDir, u.Name(), "discord", "context")
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() {
				continue
			}
			info, err := f.Info()
			if err != nil || !info.ModTime().Before(cutoff) {
				continue
			}
			_ = os.Remove(filepath.Join(dir, f.Name()))
		}
	}
}
