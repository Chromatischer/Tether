package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/ssh"
)

type TerminalProfile struct {
	Term             string
	IsIterm          bool
	Is256Color       bool
	DisableGradients bool
	Disclaimer       string
	HeaderLabel      string
	BlockMessage     string
}

func DetectTerminalProfile(s ssh.Session) TerminalProfile {
	env := sessionEnv(s)

	term := strings.TrimSpace(env["TERM"])
	if pty, _, ok := s.Pty(); ok && strings.TrimSpace(pty.Term) != "" {
		term = strings.TrimSpace(pty.Term)
	}

	return terminalProfileFromEnv(env, term)
}

// DetectLocalTerminalProfile builds a profile from the local process
// environment, for running the TUI directly in the attached terminal
// (no SSH session). It mirrors DetectTerminalProfile's logic.
func DetectLocalTerminalProfile() TerminalProfile {
	env := map[string]string{}
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return terminalProfileFromEnv(env, strings.TrimSpace(env["TERM"]))
}

func terminalProfileFromEnv(env map[string]string, term string) TerminalProfile {
	profile := TerminalProfile{
		Term:       term,
		IsIterm:    isItermTerminal(env),
		Is256Color: is256ColorTerm(term),
	}

	if profile.IsIterm {
		profile.BlockMessage = "iTerm SSH sessions are not supported. Use a different terminal."
		return profile
	}

	if profile.Is256Color {
		profile.DisableGradients = true
		profile.Disclaimer = "256-color terminal detected: gradients disabled for compatibility."
		profile.HeaderLabel = "256-color mode"
	}

	return profile
}

func (p TerminalProfile) Blocked() bool {
	return strings.TrimSpace(p.BlockMessage) != ""
}

func sessionEnv(s ssh.Session) map[string]string {
	env := map[string]string{}
	if s == nil {
		return env
	}
	for _, entry := range s.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		env[key] = value
	}
	return env
}

func isItermTerminal(env map[string]string) bool {
	for _, key := range []string{"TERM_PROGRAM", "LC_TERMINAL"} {
		if strings.Contains(strings.ToLower(strings.TrimSpace(env[key])), "iterm") {
			return true
		}
	}
	return false
}

func is256ColorTerm(term string) bool {
	term = strings.ToLower(strings.TrimSpace(term))
	return strings.Contains(term, "256color")
}
