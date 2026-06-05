# Security Policy

## ⚠️ Read this first: Tether is experimental and inherently insecure

Tether is an **AI-driven** assistant that you reach over SSH and (optionally)
through Discord and Signal. It hands a large language model real capabilities —
running tools, reading and writing files, executing commands, calling external
services, and acting on your behalf. **An LLM is not a security boundary.**
Treat everything Tether does as *attacker-influenceable* and run it accordingly.

Do **not** deploy Tether anywhere it can cause harm if it misbehaves. Assume it
*will* misbehave at some point.

## Prompt injection is a first-class risk

Because the model reads untrusted content — your messages, tool output, web
pages, file contents, Discord/Signal DMs, anything it is pointed at — any of
that text can contain instructions that hijack the model. This is called
**prompt injection**, and there is **no known complete defense** for it.

Concretely, a malicious message, file, web page, or API response can attempt to:

- make the assistant ignore its instructions and follow the attacker's;
- trick it into running destructive or attacker-chosen commands/tools;
- exfiltrate secrets, conversation history, or local data to a third party;
- take actions in linked accounts (Discord/Signal) that you did not intend;
- escalate its own access or persist changes.

Tether includes mitigations (secret redaction on inbound messages, a separate
encrypted secret store, confirmation prompts for some actions, optional
`bubblewrap` sandboxing), but these are **defense-in-depth, not guarantees**.
A sufficiently clever injection can defeat them.

## Operating guidance

- **Run with least privilege.** Use a dedicated, unprivileged service user
  (the installer does this) on a host you are willing to lose. Never run as root.
- **Isolate it.** Prefer a throwaway VM/container. Restrict outbound network
  access if you can. Keep `authorized_keys` tight and the portal password strong.
- **Keep secrets out of chat.** Do not paste API keys, tokens, or passwords into
  Discord/Signal. Use the encrypted secret store from the SSH session instead.
  Inbound redaction is best-effort and will miss things.
- **Don't link sensitive accounts.** Anything you link (Discord, Signal, API
  keys) is reachable by a hijacked model. Use low-value, scoped credentials.
- **Review actions.** Don't rubber-stamp confirmation prompts. Watch the audit
  log. Assume the model can and will be tricked.
- **No warranty.** Per the MIT `LICENSE`, this software is provided "as is."
  You run it entirely at your own risk.

## Supported versions

This is a hobby/experimental project. Only the latest commit on `main` is
maintained; older releases receive no security backports.

## Reporting a vulnerability

If you find a security issue, please report it **privately** rather than opening
a public issue:

- Email the maintainer at the address on their GitHub profile
  ([@Chromatischer](https://github.com/Chromatischer)), or
- Open a private GitHub Security Advisory on this repository.

Please include reproduction steps and impact. Because of the nature of LLM
systems, "the model can be prompt-injected" is a known and accepted limitation
rather than a reportable vulnerability — but novel, high-impact bypasses of the
specific mitigations above are very welcome.
