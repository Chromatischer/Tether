package redact

import (
	"regexp"
	"strings"
)

type Finding struct {
	Kind   string
	Reason string
}

var (
	// Very small set of common patterns. Expand carefully to avoid false positives.
	reGitHubToken = regexp.MustCompile(`\bghp_[A-Za-z0-9]{20,}\b`)
	reGitHubPat   = regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}\b`)
	reOpenAIKey   = regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}\b`)
	reSlackToken  = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	reAWSKeyID    = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	reJWT         = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\b`)

	rePasswordLine = regexp.MustCompile(`(?i)\b(pass(word)?|pwd|token|api[_-]?key|secret)\b\s*[:=]\s*([^\s]+)`) // captures value
)

func ScanAndRedact(text string) (redacted string, findings []Finding) {
	redacted = text

	repl := func(kind, reason string, re *regexp.Regexp) {
		if re.MatchString(redacted) {
			findings = append(findings, Finding{Kind: kind, Reason: reason})
			redacted = re.ReplaceAllString(redacted, "[REDACTED]")
		}
	}

	repl("token", "GitHub token", reGitHubToken)
	repl("token", "GitHub PAT", reGitHubPat)
	repl("token", "OpenAI-style API key", reOpenAIKey)
	repl("token", "Slack token", reSlackToken)
	repl("token", "AWS Access Key ID", reAWSKeyID)
	repl("token", "JWT", reJWT)

	// Redact values in 'password: <value>' lines.
	if rePasswordLine.MatchString(redacted) {
		findings = append(findings, Finding{Kind: "credential", Reason: "credential-like key/value"})
		redacted = rePasswordLine.ReplaceAllStringFunc(redacted, func(m string) string {
			// keep left side, redact right side
			idx := strings.IndexAny(m, ":=")
			if idx < 0 {
				return "[REDACTED]"
			}
			return strings.TrimSpace(m[:idx+1]) + " [REDACTED]"
		})
	}

	return redacted, findings
}
