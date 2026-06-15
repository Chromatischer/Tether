package term

import (
	"os"
	"strings"

	"github.com/mattn/go-isatty"
)

type ColorLevel int

const (
	ColorNone  ColorLevel = 0
	Color16    ColorLevel = 1
	Color256   ColorLevel = 2
	ColorTrue  ColorLevel = 3
)

type TerminalProfile struct {
	Term        string
	Width       int
	Height      int
	ColorLevel  ColorLevel
	IsTTY       bool
	TextMode    bool
	Disclaimer  string
}

func DetectLocalTerminalProfile(textMode bool) TerminalProfile {
	profile := TerminalProfile{
		TextMode: textMode,
	}

	profile.IsTTY = isatty.IsTerminal(os.Stdout.Fd())

	profile.Term = strings.TrimSpace(os.Getenv("TERM"))

	if textMode {
		profile.ColorLevel = ColorNone
		return profile
	}

	if !profile.IsTTY {
		profile.ColorLevel = ColorNone
		profile.Disclaimer = "stdout is not a terminal — color disabled."
		return profile
	}

	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		profile.ColorLevel = ColorNone
		profile.Disclaimer = "NO_COLOR set — styling disabled."
		return profile
	}

	termLower := strings.ToLower(profile.Term)

	if strings.Contains(termLower, "256color") {
		profile.ColorLevel = Color256
	} else if strings.Contains(termLower, "truecolor") || strings.Contains(termLower, "direct") || strings.Contains(termLower, "24bit") {
		profile.ColorLevel = ColorTrue
	} else {
		profile.ColorLevel = Color16
	}

	colorTerm := strings.ToLower(strings.TrimSpace(os.Getenv("COLORTERM")))
	if colorTerm == "truecolor" || colorTerm == "24bit" {
		profile.ColorLevel = ColorTrue
	}

	return profile
}
