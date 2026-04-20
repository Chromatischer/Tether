package tui

import "testing"

func TestIsItermTerminalRecognizesItermEnv(t *testing.T) {
	if !isItermTerminal(map[string]string{"TERM_PROGRAM": "iTerm.app"}) {
		t.Fatal("expected TERM_PROGRAM=iTerm.app to be treated as iTerm")
	}
	if !isItermTerminal(map[string]string{"LC_TERMINAL": "iTerm2"}) {
		t.Fatal("expected LC_TERMINAL=iTerm2 to be treated as iTerm")
	}
}

func TestIs256ColorTermRecognizes256ColorTerms(t *testing.T) {
	if !is256ColorTerm("xterm-256color") {
		t.Fatal("expected xterm-256color to be treated as a 256-color terminal")
	}
	if is256ColorTerm("xterm-direct") {
		t.Fatal("expected xterm-direct to avoid the 256-color fallback")
	}
}
