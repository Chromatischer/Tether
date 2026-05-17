package tether

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

//go:embed CHANGELOG.md
var changelogFS embed.FS

var changelogHeadingPattern = regexp.MustCompile(`^##\s+(v?[0-9]+(?:\.[0-9]+){1,2})(?:\s+.*)?$`)

type ChangelogEntry struct {
	Version string
	Title   string
	Body    string
}

func NormalizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "V")
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

func ChangelogMarkdown() (string, error) {
	b, err := changelogFS.ReadFile("CHANGELOG.md")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func ChangelogEntries() ([]ChangelogEntry, error) {
	src, err := ChangelogMarkdown()
	if err != nil {
		return nil, err
	}
	return ParseChangelog(src), nil
}

func ParseChangelog(src string) []ChangelogEntry {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	lines := strings.Split(src, "\n")
	entries := make([]ChangelogEntry, 0, 8)
	current := -1
	for _, line := range lines {
		if m := changelogHeadingPattern.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			entries = append(entries, ChangelogEntry{
				Version: NormalizeVersion(m[1]),
				Title:   strings.TrimSpace(line),
			})
			current = len(entries) - 1
			continue
		}
		if current >= 0 {
			if entries[current].Body != "" {
				entries[current].Body += "\n"
			}
			entries[current].Body += line
		}
	}
	for i := range entries {
		entries[i].Body = strings.TrimSpace(entries[i].Body)
	}
	return entries
}

func RenderChangelogFrom(version string) (string, error) {
	version = NormalizeVersion(version)
	if version == "" {
		version = CurrentVersion()
	}
	entries, err := ChangelogEntries()
	if err != nil {
		return "", err
	}
	start := -1
	for i, e := range entries {
		if e.Version == version {
			start = i
			break
		}
	}
	if start < 0 {
		return "", fmt.Errorf("unknown changelog version: %s", version)
	}
	return renderEntries(entries[:start+1]), nil
}

func RenderChangelogAfter(previousVersion string) (string, bool, error) {
	previousVersion = NormalizeVersion(previousVersion)
	current := CurrentVersion()
	if previousVersion == "" || previousVersion == current {
		return "", false, nil
	}
	entries, err := ChangelogEntries()
	if err != nil {
		return "", false, err
	}
	stop := -1
	for i, e := range entries {
		if e.Version == previousVersion {
			stop = i
			break
		}
	}
	if stop < 0 {
		return "", false, fmt.Errorf("unknown changelog version: %s", previousVersion)
	}
	if stop == 0 {
		return "", false, nil
	}
	return renderEntries(entries[:stop]), true, nil
}

func renderEntries(entries []ChangelogEntry) string {
	var b strings.Builder
	b.WriteString("# Changelog\n\n")
	for i, e := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(e.Title)
		if strings.TrimSpace(e.Body) != "" {
			b.WriteString("\n\n")
			b.WriteString(strings.TrimSpace(e.Body))
		}
	}
	return strings.TrimSpace(b.String())
}
