package discord

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"

	"tether/internal/userspace"
)

// outboundAttachmentPattern matches an attachment notation tag carrying a path,
// e.g. [attachment name="report.pdf" path=workspace/report.pdf]. It mirrors the
// inbound notation so the agent can reply with files using the same syntax.
var outboundAttachmentPattern = regexp.MustCompile(`\[attachment\b[^\]]*\bpath=[^\]]+\]`)

var blankLineRun = regexp.MustCompile(`\n{3,}`)

// outboundRef is a parsed reference to a file the agent wants to attach.
type outboundRef struct {
	Path string
	Name string
}

func (r outboundRef) displayName() string {
	if n := strings.TrimSpace(r.Name); n != "" {
		return n
	}
	if p := strings.TrimSpace(r.Path); p != "" {
		return p
	}
	return "(unknown)"
}

// parseOutboundAttachments extracts attachment tags from the assistant text and
// returns the text with those tags removed plus the parsed references.
func parseOutboundAttachments(text string) (string, []outboundRef) {
	matches := outboundAttachmentPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return text, nil
	}
	refs := make([]outboundRef, 0, len(matches))
	for _, m := range matches {
		path := strings.TrimSpace(extractTagField(m, "path"))
		if path == "" {
			continue
		}
		refs = append(refs, outboundRef{Path: path, Name: strings.TrimSpace(extractTagField(m, "name"))})
	}
	cleaned := outboundAttachmentPattern.ReplaceAllString(text, "")
	cleaned = blankLineRun.ReplaceAllString(cleaned, "\n\n")
	return strings.TrimSpace(cleaned), refs
}

// extractTagField pulls key="value" or key=value (unquoted) out of a tag.
func extractTagField(tag, key string) string {
	if m := regexp.MustCompile(regexp.QuoteMeta(key) + `="([^"]*)"`).FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	if m := regexp.MustCompile(regexp.QuoteMeta(key) + `=([^\s\]]+)`).FindStringSubmatch(tag); m != nil {
		return m[1]
	}
	return ""
}

// loadOutboundFiles resolves and reads each referenced file from the user
// sandbox, enforcing the same size/count caps as inbound ingestion. Files that
// can't be attached are reported as human-readable notes.
func (g *Gateway) loadOutboundFiles(root string, refs []outboundRef) ([]*discordgo.File, []string) {
	if len(refs) == 0 {
		return nil, nil
	}
	ac := g.cfg.Discord.Attachments
	maxBytes := int64(ac.MaxSizeMB) * 1024 * 1024

	var (
		files []*discordgo.File
		notes []string
	)
	for _, r := range refs {
		if ac.MaxPerMessage > 0 && len(files) >= ac.MaxPerMessage {
			notes = append(notes, "(could not attach "+r.displayName()+": too many attachments)")
			continue
		}
		full, err := resolveSandboxFile(root, r.Path)
		if err != nil {
			notes = append(notes, "(could not attach "+r.displayName()+": "+err.Error()+")")
			continue
		}
		fi, err := os.Stat(full)
		if err != nil {
			notes = append(notes, "(could not attach "+r.displayName()+": "+err.Error()+")")
			continue
		}
		if maxBytes > 0 && fi.Size() > maxBytes {
			notes = append(notes, fmt.Sprintf("(could not attach %s: too large, limit %dMB)", r.displayName(), ac.MaxSizeMB))
			continue
		}
		data, err := os.ReadFile(full)
		if err != nil {
			notes = append(notes, "(could not attach "+r.displayName()+": "+err.Error()+")")
			continue
		}
		name := strings.TrimSpace(r.Name)
		if name == "" {
			name = filepath.Base(r.Path)
		}
		files = append(files, &discordgo.File{Name: sanitizeFilename(name), Reader: bytes.NewReader(data)})
	}
	return files, notes
}

// finishWithAttachments finalizes the streamed reply, turning any attachment
// notation in the assistant text into real Discord file uploads on the same
// message.
func (g *Gateway) finishWithAttachments(stream *discordReplyStream, uid int64, out string) {
	ac := g.cfg.Discord.Attachments
	if ac.Enabled == nil || !*ac.Enabled {
		stream.Finish(out)
		return
	}

	cleaned, refs := parseOutboundAttachments(out)
	if len(refs) == 0 {
		stream.Finish(out)
		return
	}

	root := userspace.ForUser(g.cfg.Paths.DataDir, uid).Root
	files, notes := g.loadOutboundFiles(root, refs)

	finalText := strings.TrimSpace(cleaned)
	if len(notes) > 0 {
		if finalText != "" {
			finalText += "\n\n"
		}
		finalText += strings.Join(notes, "\n")
	}
	stream.FinishWithFiles(finalText, files)
}

// resolveSandboxFile resolves a sandbox-relative path to an absolute path under
// root, rejecting escapes, symlinked components, and non-regular files.
func resolveSandboxFile(root, rel string) (string, error) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", fmt.Errorf("empty path")
	}
	if filepath.IsAbs(rel) {
		// Accept the /work/ sandbox alias the tools use; reject other absolutes.
		if strings.HasPrefix(rel, "/work/") {
			rel = strings.TrimPrefix(rel, "/work/")
		} else {
			return "", fmt.Errorf("path must be relative")
		}
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox")
	}

	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	rootAbs = filepath.Clean(rootAbs)

	cur := rootAbs
	for _, part := range strings.Split(clean, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		if fi, err := os.Lstat(cur); err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("symlink component not allowed")
		}
	}

	full := filepath.Clean(cur)
	if full != rootAbs && !strings.HasPrefix(full, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes sandbox")
	}
	fi, err := os.Lstat(full)
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() {
		return "", fmt.Errorf("not a regular file")
	}
	return full, nil
}
