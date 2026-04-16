package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type manifest struct {
	CreatedAtUTC string `json:"created_at_utc"`
	SQLitePath   string `json:"sqlite_path"`
	SQLiteSHA256 string `json:"sqlite_sha256"`

	IncludedFiles []string `json:"included_files"`

	RequiredEnv []string `json:"required_env"`
	Notes       string   `json:"notes"`
}

func main() {
	var dbPath string
	var outDir string
	var includeConfig bool
	var includeTetherYAML bool
	var includeUserConfig bool
	var configDir string
	var dataDir string

	flag.StringVar(&dbPath, "db", "./data/tether.sqlite", "path to sqlite db")
	flag.StringVar(&outDir, "out", "./backups", "output directory")
	flag.BoolVar(&includeConfig, "include-config", false, "copy SSH portal config files into the backup output (sensitive)")
	flag.BoolVar(&includeTetherYAML, "include-config-yaml", false, "also copy config/tether.yaml (may contain API keys / secrets)")
	flag.BoolVar(&includeUserConfig, "include-user-config", false, "copy per-user config/ and skills/ dirs (not workspace)")
	flag.StringVar(&configDir, "config-dir", "./config", "config directory to copy from")
	flag.StringVar(&dataDir, "data-dir", "./data", "data directory (used for include-user-config)")
	flag.Parse()

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	stamp := time.Now().UTC().Format("20060102T150405Z")
	base := "tether-" + stamp
	outSQLite := filepath.Join(outDir, base+".sqlite")
	outManifest := filepath.Join(outDir, base+".manifest.json")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	defer db.Close()

	// Use VACUUM INTO to produce a consistent backup.
	if _, err := db.Exec(`VACUUM INTO ?`, outSQLite); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	included := []string{}
	if includeConfig {
		dst := filepath.Join(outDir, base+".config")
		_ = os.MkdirAll(dst, 0o700)
		included = append(included, copyIfExists(filepath.Join(configDir, "ssh_host_ed25519"), filepath.Join(dst, "ssh_host_ed25519"))...)
		included = append(included, copyIfExists(filepath.Join(configDir, "ssh_host_ed25519.pub"), filepath.Join(dst, "ssh_host_ed25519.pub"))...)
		included = append(included, copyIfExists(filepath.Join(configDir, "authorized_keys"), filepath.Join(dst, "authorized_keys"))...)
		if includeTetherYAML {
			included = append(included, copyIfExists(filepath.Join(configDir, "tether.yaml"), filepath.Join(dst, "tether.yaml"))...)
		}
	}

	if includeUserConfig {
		usersDir := filepath.Join(dataDir, "users")
		entries, _ := os.ReadDir(usersDir)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			uid := e.Name()
			srcCfg := filepath.Join(usersDir, uid, "config")
			srcSkills := filepath.Join(usersDir, uid, "skills")
			dstRoot := filepath.Join(outDir, base+".users", uid)
			included = append(included, copyDirIfExists(srcCfg, filepath.Join(dstRoot, "config"))...)
			included = append(included, copyDirIfExists(srcSkills, filepath.Join(dstRoot, "skills"))...)
		}
	}

	sum, _ := sha256File(outSQLite)
	m := manifest{
		CreatedAtUTC:  time.Now().UTC().Format(time.RFC3339),
		SQLitePath:    outSQLite,
		SQLiteSHA256:  sum,
		IncludedFiles: included,
		RequiredEnv:   []string{"TETHER_MASTER_KEY", "OPENROUTER_API_KEY", "TETHER_SIGNAL_NUMBER", "TETHER_DISCORD_BOT_TOKEN"},
		Notes: strings.TrimSpace("" +
			"This backup contains a consistent SQLite snapshot.\n" +
			"It does NOT include environment variables or OS-level secrets by default.\n" +
			"For restore you typically need: TETHER_MASTER_KEY (for secrets decryption), OpenRouter API key, and (if used) Signal/Discord integration config/state.\n" +
			"Also consider backing up the Signal data directory used by signal-cli (often ~/.local/share/signal-cli).\n"),
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	_ = os.WriteFile(outManifest, b, 0o600)

	fmt.Println(outSQLite)
	fmt.Fprintln(os.Stderr, "Wrote manifest:", outManifest)
	fmt.Fprintln(os.Stderr, "Note: also back up TETHER_MASTER_KEY (and signal-cli data dir if using Signal).")
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyIfExists(src, dst string) []string {
	fi, err := os.Lstat(src)
	if err != nil {
		return nil
	}
	// Avoid copying symlinks (prevents backup from unexpectedly including arbitrary files).
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	if !fi.Mode().IsRegular() {
		return nil
	}
	_ = os.MkdirAll(filepath.Dir(dst), 0o700)
	if err := copyFile(src, dst, fi.Mode().Perm()); err != nil {
		return nil
	}
	return []string{dst}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func copyDirIfExists(srcDir, dstDir string) []string {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return nil
	}
	_ = os.MkdirAll(dstDir, 0o700)
	out := []string{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		src := filepath.Join(srcDir, e.Name())
		dst := filepath.Join(dstDir, e.Name())
		out = append(out, copyIfExists(src, dst)...)
	}
	return out
}
