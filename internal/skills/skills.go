package skills

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/anmitsu/go-shlex"
	"gopkg.in/yaml.v3"

	"tether/internal/redact"
	"tether/internal/sandbox"
	"tether/internal/userspace"
)

//go:embed bundled/**
var bundledFS embed.FS

type Source string

const (
	SourceBundled Source = "bundled"
	SourceUser    Source = "user"
	SourceProject Source = "project"
)

// Skill is a Claude Code–style skill (directory with SKILL.md + optional supporting files).
//
// We load only lightweight metadata during discovery.
type Skill struct {
	Name        string
	Description string
	WhenToUse   string

	DisableModelInvocation bool
	UserInvocable          bool

	AllowedTools []string
	Model        string
	Effort       string
	Context      string // e.g. "fork" (best-effort)
	Agent        string
	Shell        string // bash|powershell (we support bash)
	Paths        []string

	Source Source

	// Host absolute paths for disk-backed skills.
	DirHostAbs   string
	EntryHostAbs string

	// For bundled skills.
	bundledRelDir string // e.g. "bundled/simplify"
}

type Frontmatter struct {
	Name               string `yaml:"name"`
	Description        string `yaml:"description"`
	WhenToUse          string `yaml:"when_to_use"`
	ArgumentHint       string `yaml:"argument-hint"`
	DisableModelInvoke bool   `yaml:"disable-model-invocation"`
	UserInvocable      *bool  `yaml:"user-invocable"`

	AllowedTools any    `yaml:"allowed-tools"`
	Model        string `yaml:"model"`
	Effort       string `yaml:"effort"`
	Context      string `yaml:"context"`
	Agent        string `yaml:"agent"`
	Shell        string `yaml:"shell"`
	Paths        any    `yaml:"paths"`
}

// Confirmer is the subset of confirmation behavior skills need.
// Implemented by agent/toolset.Session.Confirm.
type Confirmer interface {
	Consume(userID int64, token string, scope string) bool
	Request(userID int64, scope string, reason string) string
}

type Manager struct {
	IndexCharBudget int
}

func NewManager() *Manager { return &Manager{IndexCharBudget: 8000} }

// List returns the merged set of skills for this user.
// Conflict resolution: user > project > bundled.
func (m *Manager) List(d userspace.Dirs) ([]Skill, error) {
	bundled, _ := m.listBundled()
	user, _ := m.listUser(d)
	project, _ := m.listProject(d)

	byName := map[string]Skill{}
	for _, s := range bundled {
		byName[s.Name] = s
	}
	for _, s := range project {
		byName[s.Name] = s
	}
	for _, s := range user {
		byName[s.Name] = s
	}

	out := make([]Skill, 0, len(byName))
	for _, s := range byName {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// BuildIndexMessage returns a compact listing (for the model).
// Per Claude Code docs, skills with disable-model-invocation are not listed to the model.
func (m *Manager) BuildIndexMessage(skills []Skill) string {
	budget := m.IndexCharBudget
	if budget <= 0 {
		budget = 8000
	}

	var b strings.Builder
	b.WriteString("Skills available (invoke with tool skill.invoke {name, arguments}):\n")
	for _, s := range skills {
		if s.DisableModelInvocation {
			continue
		}
		line := "- /" + s.Name
		desc := strings.TrimSpace(s.Description)
		when := strings.TrimSpace(s.WhenToUse)
		if desc != "" {
			line += ": " + desc
			if when != "" {
				line += " " + when
			}
		}
		if len(line) > 1536 {
			line = line[:1536] + "…"
		}
		if b.Len()+len(line)+1 > budget {
			b.WriteString("(skills list truncated)\n")
			break
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func (m *Manager) Resolve(skills []Skill, name string) (Skill, bool) {
	name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	name = strings.ToLower(name)
	if name == "" {
		return Skill{}, false
	}
	for _, s := range skills {
		if strings.EqualFold(s.Name, name) {
			return s, true
		}
	}
	return Skill{}, false
}

type Invocation struct {
	SkillName          string
	Content            string
	SkillDirSandboxAbs string // expanded ${CLAUDE_SKILL_DIR}
}

type InvokeOptions struct {
	UserID       int64
	Arguments    string
	Invoker      string // "user" | "model"
	SessionID    string
	ConfirmToken string
}

// Invoke loads and renders the skill content.
// It applies Claude Code substitutions and executes !`cmd` / ```! shell injections
// inside Tether's existing no-network sandbox.
func (m *Manager) Invoke(ctx context.Context, d userspace.Dirs, confirmer Confirmer, s Skill, opt InvokeOptions) (Invocation, error) {
	invoker := strings.TrimSpace(strings.ToLower(opt.Invoker))
	if invoker == "" {
		invoker = "model"
	}
	if invoker == "model" && s.DisableModelInvocation {
		return Invocation{}, fmt.Errorf("skill is not model-invocable: %s", s.Name)
	}
	if invoker == "user" && !s.UserInvocable {
		return Invocation{}, fmt.Errorf("skill is not user-invocable: %s", s.Name)
	}

	s, err := m.ensureBundledExtracted(d, s)
	if err != nil {
		return Invocation{}, err
	}
	raw, err := os.ReadFile(s.EntryHostAbs)
	if err != nil {
		return Invocation{}, err
	}
	fm, body := splitFrontmatter(string(raw))
	if strings.TrimSpace(body) == "" {
		return Invocation{}, errors.New("empty SKILL.md")
	}

	hadArgumentsPlaceholder := strings.Contains(body, "$ARGUMENTS")

	// Compute ${CLAUDE_SKILL_DIR} sandbox-visible path.
	skillDirSandbox := toSandboxPath(d.Root, s.DirHostAbs)

	rendered, err := applySubstitutions(body, opt.Arguments, opt.SessionID, skillDirSandbox)
	if err != nil {
		return Invocation{}, err
	}

	rendered, err = applyShellInjections(ctx, d, confirmer, opt.UserID, rendered, opt.ConfirmToken)
	if err != nil {
		return Invocation{}, err
	}

	// Claude Code behavior: if args were passed but $ARGUMENTS is not present, append them.
	if strings.TrimSpace(opt.Arguments) != "" && !hadArgumentsPlaceholder {
		rendered = strings.TrimSpace(rendered) + "\n\nARGUMENTS: " + strings.TrimSpace(opt.Arguments)
	}

	// If skill frontmatter sets shell, we currently only support bash; ignore otherwise.
	_ = fm

	return Invocation{SkillName: s.Name, Content: strings.TrimSpace(rendered), SkillDirSandboxAbs: skillDirSandbox}, nil
}

func applySubstitutions(body string, arguments string, sessionID string, skillDirSandboxAbs string) (string, error) {
	// Parse args with shell quoting semantics.
	args := []string{}
	if strings.TrimSpace(arguments) != "" {
		parts, err := shlex.Split(arguments, true)
		if err == nil {
			args = parts
		}
	}

	out := body
	out = strings.ReplaceAll(out, "${CLAUDE_SESSION_ID}", sessionID)
	out = strings.ReplaceAll(out, "${CLAUDE_SKILL_DIR}", skillDirSandboxAbs)

	// Important: replace positional forms before $ARGUMENTS, otherwise $ARGUMENTS[0] would be corrupted.
	for i := 0; i < 64; i++ {
		val := ""
		if i < len(args) {
			val = args[i]
		}
		out = strings.ReplaceAll(out, fmt.Sprintf("$ARGUMENTS[%d]", i), val)
		out = strings.ReplaceAll(out, fmt.Sprintf("$%d", i), val)
	}
	out = strings.ReplaceAll(out, "$ARGUMENTS", arguments)
	return out, nil
}

func applyShellInjections(ctx context.Context, d userspace.Dirs, confirmer Confirmer, userID int64, content string, confirmToken string) (string, error) {
	blockRe, err := regexp.Compile("(?s)```!\\s*\\n(.*?)\\n```\\s*")
	if err != nil {
		return "", err
	}
	inlineRe, err := regexp.Compile("!`([^`]+)`")
	if err != nil {
		return "", err
	}

	cmds := []string{}
	for _, m := range blockRe.FindAllStringSubmatch(content, -1) {
		cmds = append(cmds, strings.TrimSpace(m[1]))
	}
	for _, m := range inlineRe.FindAllStringSubmatch(content, -1) {
		cmds = append(cmds, strings.TrimSpace(m[1]))
	}

	needsConfirm := false
	for _, c := range cmds {
		if looksDestructive(c) {
			needsConfirm = true
			break
		}
	}
	if needsConfirm {
		scope := skillShellConfirmScope(cmds)
		if confirmer == nil || !confirmer.Consume(userID, strings.TrimSpace(confirmToken), scope) {
			return "", fmt.Errorf("skill shell injection requires confirmation; scope=%q", scope)
		}
	}

	run := func(cmd string) (string, error) {
		cmd = strings.TrimSpace(cmd)
		if cmd == "" {
			return "", nil
		}
		// Commands start in /work (sandbox root). For repo commands inside workspace/: cd workspace && ...
		res, err := sandbox.RunNoNet(ctx, d.Root, []string{"bash", "-lc", cmd})
		if err != nil {
			return "", err
		}
		out := res.Stdout
		if strings.TrimSpace(res.Stderr) != "" {
			out = strings.TrimRight(out, "\n") + "\n" + res.Stderr
		}
		clean, _ := redact.ScanAndRedact(out)
		clean = strings.TrimSpace(clean)
		if res.ExitCode != 0 {
			return clean, fmt.Errorf("exit %d", res.ExitCode)
		}
		return clean, nil
	}

	// Replace blocks first.
	out := blockRe.ReplaceAllStringFunc(content, func(block string) string {
		sub := blockRe.FindStringSubmatch(block)
		if len(sub) < 2 {
			return block
		}
		val, err := run(sub[1])
		if err != nil {
			if strings.TrimSpace(val) == "" {
				return "[shell error: " + err.Error() + "]"
			}
			return "[shell error: " + err.Error() + "]\n" + val
		}
		if val == "" {
			return "(no output)"
		}
		return val
	})

	out = inlineRe.ReplaceAllStringFunc(out, func(tok string) string {
		sub := inlineRe.FindStringSubmatch(tok)
		if len(sub) < 2 {
			return tok
		}
		val, err := run(sub[1])
		if err != nil {
			if strings.TrimSpace(val) == "" {
				return "[shell error: " + err.Error() + "]"
			}
			return "[shell error: " + err.Error() + "] " + val
		}
		if val == "" {
			return "(no output)"
		}
		return val
	})

	return out, nil
}

func skillShellConfirmScope(cmds []string) string {
	joined := strings.TrimSpace(strings.Join(cmds, "\n"))
	sum := sha256.Sum256([]byte(joined))
	h := hex.EncodeToString(sum[:8])
	preview := strings.ReplaceAll(joined, "\n", " ; ")
	preview = strings.TrimSpace(preview)
	if len(preview) > 80 {
		preview = preview[:80] + "…"
	}
	if preview == "" {
		preview = "(empty)"
	}
	return "skill:shell:" + h + ":" + preview
}

func looksDestructive(cmd string) bool {
	c := strings.ToLower(cmd)
	for _, kw := range []string{"rm ", "rm\t", "mv ", "mv\t", "chmod ", "chown ", "sed -i", "truncate ", "dd if=", "mkfs", "shutdown", "reboot"} {
		if strings.Contains(c, kw) {
			return true
		}
	}
	return false
}

func splitFrontmatter(raw string) (Frontmatter, string) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "---\n") {
		return Frontmatter{}, raw
	}
	end := strings.Index(s[4:], "\n---\n")
	if end == -1 {
		return Frontmatter{}, raw
	}
	front := s[4 : 4+end]
	body := s[4+end+5:]
	var fm Frontmatter
	_ = yaml.Unmarshal([]byte(front), &fm)
	return fm, strings.TrimSpace(body)
}

func toSandboxPath(rootHostAbs string, pathHostAbs string) string {
	rootHostAbs, _ = filepath.Abs(rootHostAbs)
	pathHostAbs, _ = filepath.Abs(pathHostAbs)
	rel, err := filepath.Rel(rootHostAbs, pathHostAbs)
	if err != nil {
		return "/work"
	}
	rel = filepath.ToSlash(rel)
	rel = strings.TrimPrefix(rel, "./")
	if rel == "" || rel == "." {
		return "/work"
	}
	return "/work/" + rel
}

func (m *Manager) listBundled() ([]Skill, error) {
	out := []Skill{}
	_ = fs.WalkDir(bundledFS, "bundled", func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if de.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, "/SKILL.md") {
			return nil
		}
		parts := strings.Split(path, "/")
		if len(parts) < 3 {
			return nil
		}
		dir := strings.Join(parts[:len(parts)-1], "/")
		name := parts[len(parts)-2]
		norm, ok := normalizeSkillName(name)
		if !ok {
			return nil
		}
		b, err := bundledFS.ReadFile(path)
		if err != nil {
			return nil
		}
		fm, body := splitFrontmatter(string(b))
		desc := strings.TrimSpace(fm.Description)
		when := strings.TrimSpace(fm.WhenToUse)
		if desc == "" {
			desc = firstParagraph(body)
		}
		userInv := true
		if fm.UserInvocable != nil {
			userInv = *fm.UserInvocable
		}
		out = append(out, Skill{
			Name:                   coalesce(strings.TrimSpace(fm.Name), norm),
			Description:            desc,
			WhenToUse:              when,
			DisableModelInvocation: fm.DisableModelInvoke,
			UserInvocable:          userInv,
			AllowedTools:           parseStringList(fm.AllowedTools),
			Model:                  strings.TrimSpace(fm.Model),
			Effort:                 strings.TrimSpace(fm.Effort),
			Context:                strings.TrimSpace(fm.Context),
			Agent:                  strings.TrimSpace(fm.Agent),
			Shell:                  strings.TrimSpace(fm.Shell),
			Paths:                  parseStringList(fm.Paths),
			Source:                 SourceBundled,
			bundledRelDir:          dir,
		})
		return nil
	})
	return out, nil
}

func (m *Manager) listUser(d userspace.Dirs) ([]Skill, error) {
	out := []Skill{}
	entries, err := os.ReadDir(d.Skills)
	if err != nil {
		return out, nil
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		norm, ok := normalizeSkillName(e.Name())
		if !ok {
			continue
		}
		dir := filepath.Join(d.Skills, e.Name())
		entry := filepath.Join(dir, "SKILL.md")
		b, err := os.ReadFile(entry)
		if err != nil {
			continue
		}
		fm, body := splitFrontmatter(string(b))
		desc := strings.TrimSpace(fm.Description)
		when := strings.TrimSpace(fm.WhenToUse)
		if desc == "" {
			desc = firstParagraph(body)
		}
		userInv := true
		if fm.UserInvocable != nil {
			userInv = *fm.UserInvocable
		}
		out = append(out, Skill{
			Name:                   coalesce(strings.TrimSpace(fm.Name), norm),
			Description:            desc,
			WhenToUse:              when,
			DisableModelInvocation: fm.DisableModelInvoke,
			UserInvocable:          userInv,
			AllowedTools:           parseStringList(fm.AllowedTools),
			Model:                  strings.TrimSpace(fm.Model),
			Effort:                 strings.TrimSpace(fm.Effort),
			Context:                strings.TrimSpace(fm.Context),
			Agent:                  strings.TrimSpace(fm.Agent),
			Shell:                  strings.TrimSpace(fm.Shell),
			Paths:                  parseStringList(fm.Paths),
			Source:                 SourceUser,
			DirHostAbs:             dir,
			EntryHostAbs:           entry,
		})
	}
	return out, nil
}

func (m *Manager) listProject(d userspace.Dirs) ([]Skill, error) {
	out := []Skill{}
	root := strings.TrimSpace(d.Workspace)
	if root == "" {
		return out, nil
	}
	_ = filepath.WalkDir(root, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if de.IsDir() {
			switch de.Name() {
			case ".git", "node_modules", ".venv", "venv", "dist", "build":
				return filepath.SkipDir
			}
			return nil
		}
		if de.Name() != "SKILL.md" {
			return nil
		}
		p := filepath.ToSlash(path)
		idx := strings.Index(p, "/.claude/skills/")
		if idx == -1 {
			return nil
		}
		suffix := p[idx+len("/.claude/skills/"):]
		parts := strings.Split(suffix, "/")
		if len(parts) < 2 {
			return nil
		}
		norm, ok := normalizeSkillName(parts[0])
		if !ok {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		fm, body := splitFrontmatter(string(b))
		desc := strings.TrimSpace(fm.Description)
		when := strings.TrimSpace(fm.WhenToUse)
		if desc == "" {
			desc = firstParagraph(body)
		}
		userInv := true
		if fm.UserInvocable != nil {
			userInv = *fm.UserInvocable
		}
		out = append(out, Skill{
			Name:                   coalesce(strings.TrimSpace(fm.Name), norm),
			Description:            desc,
			WhenToUse:              when,
			DisableModelInvocation: fm.DisableModelInvoke,
			UserInvocable:          userInv,
			AllowedTools:           parseStringList(fm.AllowedTools),
			Model:                  strings.TrimSpace(fm.Model),
			Effort:                 strings.TrimSpace(fm.Effort),
			Context:                strings.TrimSpace(fm.Context),
			Agent:                  strings.TrimSpace(fm.Agent),
			Shell:                  strings.TrimSpace(fm.Shell),
			Paths:                  parseStringList(fm.Paths),
			Source:                 SourceProject,
			DirHostAbs:             filepath.Dir(path),
			EntryHostAbs:           path,
		})
		return nil
	})
	return out, nil
}

func (m *Manager) ensureBundledExtracted(d userspace.Dirs, s Skill) (Skill, error) {
	if s.Source != SourceBundled {
		return s, nil
	}
	name, ok := normalizeSkillName(s.Name)
	if !ok {
		return s, fmt.Errorf("invalid bundled skill name: %q", s.Name)
	}
	if strings.TrimSpace(s.bundledRelDir) == "" {
		return s, errors.New("bundled skill missing embedded dir")
	}

	dstDir := filepath.Join(d.Cache, "bundled_skills", name)
	entry := filepath.Join(dstDir, "SKILL.md")
	if _, err := os.Stat(entry); err == nil {
		s.DirHostAbs = dstDir
		s.EntryHostAbs = entry
		return s, nil
	}
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return s, err
	}

	prefix := strings.TrimSuffix(strings.TrimSpace(s.bundledRelDir), "/")
	_ = fs.WalkDir(bundledFS, prefix, func(path string, de fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel := strings.TrimPrefix(path, prefix)
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		dst := filepath.Join(dstDir, filepath.FromSlash(rel))
		if de.IsDir() {
			_ = os.MkdirAll(dst, 0o755)
			return nil
		}
		b, err := bundledFS.ReadFile(path)
		if err != nil {
			return nil
		}
		_ = os.MkdirAll(filepath.Dir(dst), 0o755)
		_ = os.WriteFile(dst, b, 0o644)
		return nil
	})

	if _, err := os.Stat(entry); err != nil {
		return s, fmt.Errorf("bundled skill extraction failed: %w", err)
	}
	s.DirHostAbs = dstDir
	s.EntryHostAbs = entry
	return s, nil
}

func parseStringList(v any) []string {
	out := []string{}
	switch x := v.(type) {
	case nil:
		return out
	case string:
		// For allowed-tools, Claude Code allows space-separated strings; we keep it simple.
		for _, p := range strings.Fields(x) {
			p = strings.TrimSpace(p)
			if p != "" {
				out = append(out, p)
			}
		}
	case []any:
		for _, it := range x {
			if s, ok := it.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

func firstParagraph(body string) string {
	body = strings.TrimSpace(body)
	if body == "" {
		return ""
	}
	parts := strings.Split(body, "\n\n")
	return strings.TrimSpace(parts[0])
}

func normalizeSkillName(name string) (string, bool) {
	name = strings.TrimSpace(strings.ToLower(strings.TrimPrefix(name, "/")))
	if name == "" || len(name) > 64 {
		return "", false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			continue
		}
		return "", false
	}
	return name, true
}

func coalesce(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return strings.TrimSpace(a)
	}
	return strings.TrimSpace(b)
}
