package userspace

import (
	"fmt"
	"os"
	"path/filepath"
)

type Dirs struct {
	Root      string
	Workspace string
	Config    string
	Skills    string
	Cache     string
}

func ForUser(dataDir string, userID int64) Dirs {
	root := filepath.Join(dataDir, "users", fmt.Sprintf("%d", userID))
	return Dirs{
		Root:      root,
		Workspace: filepath.Join(root, "workspace"),
		Config:    filepath.Join(root, "config"),
		Skills:    filepath.Join(root, "skills"),
		Cache:     filepath.Join(root, "cache"),
	}
}

func Ensure(d Dirs) error {
	for _, p := range []string{d.Root, d.Workspace, d.Config, d.Skills, d.Cache} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
	}
	return nil
}
