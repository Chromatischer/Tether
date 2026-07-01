package toolset

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"tether/internal/tools"

	"golang.org/x/sys/unix"
)

// ViewImage loads an image from the user sandbox so a vision-capable model can
// see it. The image is attached to the conversation as image content; the tool
// itself returns a small text acknowledgement.
type ViewImage struct{}

type viewImageArgs struct {
	Path string `json:"path"`
}

// maxViewImageBytes caps the size of an image loaded for viewing. Vision models
// reject very large payloads and base64 inflates size ~33%, so keep this modest.
const maxViewImageBytes = 8 * 1024 * 1024

func (t ViewImage) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name:    "view_image",
		Summary: "Load an image from the sandbox so you can see it (vision models only).",
		Safety:  "Read-only. Only available on vision-capable models. Path must stay within the sandbox; non-image and oversized files are rejected.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "minLength": 1, "description": "relative path to an image under the user sandbox root"},
			},
			"required": []string{"path"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"path":   map[string]any{"type": "string"},
				"type":   map[string]any{"type": "string", "description": "detected image MIME type"},
				"bytes":  map[string]any{"type": "integer"},
				"status": map[string]any{"type": "string"},
			},
			"required": []string{"path", "type", "bytes", "status"},
		},
		Examples: []tools.ToolExample{
			{
				Title:  "View a Discord image attachment",
				Args:   map[string]any{"path": "discord/context/a1b2c3d4.jpg"},
				Result: map[string]any{"path": "discord/context/a1b2c3d4.jpg", "type": "image/jpeg", "bytes": 148213, "status": "attached"},
				Notes:  "The image content is now attached to the conversation.",
			},
		},
		Tags: []string{"fs", "sandbox", "vision"},
	}
}

func (t ViewImage) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t ViewImage) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	_ = ctx
	if s == nil || !s.VisionEnabled {
		return nil, fmt.Errorf("view_image requires a vision-capable model")
	}
	var args viewImageArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	p, err := resolveUnderRoot(s.Dirs.Root, args.Path)
	if err != nil {
		return nil, err
	}
	relPath := normalizeSandboxRelPath(args.Path)

	fd, err := unix.Open(p, unix.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), "view_image")
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	if fi.Size() > maxViewImageBytes {
		return nil, fmt.Errorf("image too large (%d bytes, limit %d)", fi.Size(), maxViewImageBytes)
	}

	b, err := io.ReadAll(io.LimitReader(f, maxViewImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxViewImageBytes {
		return nil, fmt.Errorf("image too large (limit %d bytes)", maxViewImageBytes)
	}

	mime := detectImageMIME(b)
	if mime == "" {
		return nil, fmt.Errorf("file is not a recognized image")
	}

	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	s.AttachImage(PendingImage{DataURL: dataURL, Path: relPath})
	s.MarkReadPath(relPath)

	return map[string]any{
		"path":   args.Path,
		"type":   mime,
		"bytes":  len(b),
		"status": "attached",
	}, nil
}

// detectImageMIME returns the image MIME type for the given bytes, or "" if the
// content is not a supported image format.
func detectImageMIME(b []byte) string {
	ct := http.DetectContentType(b)
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.TrimSpace(strings.ToLower(ct))
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return ct
	default:
		return ""
	}
}
