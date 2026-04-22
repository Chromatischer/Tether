package skills

import (
	"strings"
	"testing"
)

func TestBuildIndexMessageSkipsDisabledAndTruncates(t *testing.T) {
	m := &Manager{IndexCharBudget: 140}
	msg := m.BuildIndexMessage([]Skill{
		{Name: "visible", Description: "Visible skill.", WhenToUse: "Use it often."},
		{Name: "second-visible", Description: strings.Repeat("Long line ", 12)},
		{Name: "hidden", Description: "Hidden skill.", DisableModelInvocation: true},
	})

	if !strings.Contains(msg, "/visible") {
		t.Fatalf("expected visible skill in index: %q", msg)
	}
	if strings.Contains(msg, "/hidden") {
		t.Fatalf("disabled model-invocable skill should be omitted: %q", msg)
	}
	if !strings.Contains(msg, "(skills list truncated)") {
		t.Fatalf("expected truncated marker in compact budget: %q", msg)
	}
}

func TestResolveNormalizeSkillNameAndCoalesce(t *testing.T) {
	m := NewManager()
	s, ok := m.Resolve([]Skill{{Name: "alpha-tool"}}, "/ALPHA-TOOL")
	if !ok || s.Name != "alpha-tool" {
		t.Fatalf("expected case-insensitive slash-tolerant resolve, got %+v ok=%v", s, ok)
	}

	if got, ok := normalizeSkillName("/Build-Docs"); !ok || got != "build-docs" {
		t.Fatalf("unexpected normalized skill name: %q ok=%v", got, ok)
	}
	for _, input := range []string{"", "two words", "bad_name", "bad/slash"} {
		if _, ok := normalizeSkillName(input); ok {
			t.Fatalf("expected invalid skill name for %q", input)
		}
	}

	if got := coalesce("  primary ", "fallback"); got != "primary" {
		t.Fatalf("unexpected coalesce result: %q", got)
	}
	if got := coalesce(" ", " fallback "); got != "fallback" {
		t.Fatalf("unexpected fallback coalesce result: %q", got)
	}
}

func TestApplySubstitutions(t *testing.T) {
	body := "session=${CLAUDE_SESSION_ID}\ndir=${CLAUDE_SKILL_DIR}\nzero=$0 first=$1 all=$ARGUMENTS named=$ARGUMENTS[1]"
	got, err := applySubstitutions(body, `alpha "two words"`, "sess-7", "/work/skills/demo")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"session=sess-7",
		"dir=/work/skills/demo",
		"zero=alpha",
		"first=two words",
		`all=alpha "two words"`,
		"named=two words",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected substitution %q in %q", want, got)
		}
	}
}

func TestSkillShellHelpers(t *testing.T) {
	if !looksDestructive("rm -rf /tmp/demo") {
		t.Fatal("expected destructive command to require confirmation")
	}
	if looksDestructive("printf hello") {
		t.Fatal("non-destructive command was incorrectly flagged")
	}

	scope := skillShellConfirmScope([]string{"echo one", "echo two"})
	if !strings.HasPrefix(scope, "skill:shell:") {
		t.Fatalf("unexpected scope prefix: %q", scope)
	}
	if !strings.Contains(scope, "echo one ; echo two") {
		t.Fatalf("expected readable scope preview, got %q", scope)
	}
}

func TestSplitFrontmatterAndSkillFromFrontmatter(t *testing.T) {
	raw := `---
name: Custom Skill
description: ""
when_to_use: Use this when needed.
disable-model-invocation: true
user-invocable: false
allowed-tools:
  - bash
paths:
  - scripts/run.sh
---

First paragraph.

Second paragraph.`

	fm, body := splitFrontmatter(raw)
	if strings.TrimSpace(body) != "First paragraph.\n\nSecond paragraph." {
		t.Fatalf("unexpected body: %q", body)
	}

	skill := skillFromFrontmatter("default-name", fm, body)
	if skill.Name != "Custom Skill" {
		t.Fatalf("expected explicit frontmatter name, got %q", skill.Name)
	}
	if skill.Description != "First paragraph." {
		t.Fatalf("expected first paragraph fallback, got %q", skill.Description)
	}
	if skill.WhenToUse != "Use this when needed." {
		t.Fatalf("unexpected when_to_use: %q", skill.WhenToUse)
	}
	if skill.UserInvocable || !skill.DisableModelInvocation {
		t.Fatalf("expected frontmatter booleans applied, got %+v", skill)
	}
	if len(skill.AllowedTools) != 1 || skill.AllowedTools[0] != "bash" {
		t.Fatalf("unexpected allowed tools: %+v", skill.AllowedTools)
	}
	if len(skill.Paths) != 1 || skill.Paths[0] != "scripts/run.sh" {
		t.Fatalf("unexpected paths: %+v", skill.Paths)
	}
}

func TestParseStringListFirstParagraphAndSandboxPath(t *testing.T) {
	if got := parseStringList(" bash  fetch "); strings.Join(got, ",") != "bash,fetch" {
		t.Fatalf("unexpected parsed string list: %+v", got)
	}
	if got := parseStringList([]any{" bash ", 42, "fetch"}); strings.Join(got, ",") != "bash,fetch" {
		t.Fatalf("unexpected parsed array list: %+v", got)
	}

	if got := firstParagraph("One.\n\nTwo."); got != "One." {
		t.Fatalf("unexpected first paragraph: %q", got)
	}
	if got := toSandboxPath("/tmp/root", "/tmp/root/sub/file.txt"); got != "/work/sub/file.txt" {
		t.Fatalf("unexpected sandbox path: %q", got)
	}
}
