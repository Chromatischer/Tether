package main

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/personality"
	"tether/internal/proactive"
	"tether/internal/redact"
	"tether/internal/store"
	"tether/internal/userspace"
)

func runTestCLI(ctx context.Context, cfg *config.Config, db *sql.DB, ag *agent.Agent, username, password string, in io.Reader, out io.Writer, errOut io.Writer) error {
	user, err := ensureTestUser(db, strings.TrimSpace(username), password)
	if err != nil {
		return err
	}
	conv, err := prepareTestConversation(cfg, db, user)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "TEST-CLI user=%s conversation=%d\n", user.Username, conv.ID)
	fmt.Fprintln(out, "Enter messages. Commands: /confirm <token>, /clear, /resume <code>, /quit")

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for {
		if !sc.Scan() {
			if err := sc.Err(); err != nil {
				return err
			}
			return nil
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if line == "/quit" || line == "/exit" {
			return nil
		}

		nextConv, handled, err := handleTestCLICommand(ctx, db, ag, user, conv, line, out, errOut)
		if err != nil {
			return err
		}
		if nextConv != nil {
			conv = nextConv
		}
		if handled {
			continue
		}

		if ag.HasPendingConfirmation(user.ID, conv.ID) {
			_ = ag.RejectPendingConfirmation(user.ID, conv.ID)
			_ = store.AddMessage(db, conv.ID, "system", "Pending tool confirmation rejected by the user.")
		}

		if err := store.AddMessage(db, conv.ID, "user", line); err != nil {
			return err
		}
		reply, err := ag.ReplyStream(ctx, agent.ReplyParams{
			UserID:         user.ID,
			ConversationID: conv.ID,
			Text:           line,
		}, func(ev agent.StreamEvent) {
			if ev.Type == "tool_call" && strings.TrimSpace(ev.Tool.Name) != "" {
				if strings.TrimSpace(ev.Tool.Args) != "" {
					fmt.Fprintf(errOut, "[tool] %s %s\n", ev.Tool.Name, ev.Tool.Args)
				} else {
					fmt.Fprintf(errOut, "[tool] %s\n", ev.Tool.Name)
				}
			}
		})
		if err != nil {
			return err
		}
		if err := writeAssistantReply(db, conv.ID, reply.Text, out); err != nil {
			return err
		}
	}
}

func handleTestCLICommand(ctx context.Context, db *sql.DB, ag *agent.Agent, user *store.User, conv *store.Conversation, line string, out io.Writer, _ io.Writer) (*store.Conversation, bool, error) {
	fields := strings.Fields(line)
	if len(fields) == 0 || !strings.HasPrefix(fields[0], "/") {
		return conv, false, nil
	}
	switch fields[0] {
	case "/confirm":
		if len(fields) != 2 {
			fmt.Fprintln(out, "usage: /confirm <token>")
			return conv, true, nil
		}
		if err := store.AddMessage(db, conv.ID, "user", line); err != nil {
			return conv, true, err
		}
		reply, _, resumed, err := ag.ResumeConfirmedStream(ctx, user.ID, fields[1], nil)
		if err != nil {
			return conv, true, err
		}
		if resumed {
			return conv, true, writeAssistantReply(db, conv.ID, reply.Text, out)
		}
		if ag.ConfirmToken(user.ID, fields[1]) {
			fmt.Fprintln(out, "confirmed")
			_ = store.AddMessage(db, conv.ID, "assistant", "confirmed")
			return conv, true, nil
		}
		fmt.Fprintln(out, "confirmation failed")
		_ = store.AddMessage(db, conv.ID, "assistant", "confirmation failed")
		return conv, true, nil

	case "/clear":
		if err := store.AddMessage(db, conv.ID, "user", line); err != nil {
			return conv, true, err
		}
		next, err := store.CreateConversation(db, user.ID, "")
		if err != nil {
			return conv, true, err
		}
		if err := store.SetActiveConversation(db, user.ID, next.ID); err != nil {
			return conv, true, err
		}
		ag.ResetConversationSession(next.ID)
		msg := "Started a fresh conversation with a clean agent context. Resume the previous chat with `/resume " + store.EncodeResumeCode(conv.ID) + "`."
		fmt.Fprintln(out, msg)
		_ = store.AddMessage(db, next.ID, "assistant", msg)
		return next, true, nil

	case "/resume":
		if len(fields) != 2 {
			fmt.Fprintln(out, "usage: /resume <code>")
			return conv, true, nil
		}
		if err := store.AddMessage(db, conv.ID, "user", line); err != nil {
			return conv, true, err
		}
		convID, err := store.DecodeResumeCode(fields[1])
		if err != nil {
			fmt.Fprintln(out, err.Error())
			return conv, true, nil
		}
		next, ok, err := store.GetConversation(db, user.ID, convID)
		if err != nil {
			return conv, true, err
		}
		if !ok {
			fmt.Fprintln(out, "conversation not found for that resume code")
			return conv, true, nil
		}
		if err := store.SetActiveConversation(db, user.ID, next.ID); err != nil {
			return conv, true, err
		}
		fmt.Fprintf(out, "Resumed conversation %s.\n", store.EncodeResumeCode(next.ID))
		return next, true, nil
	}
	return conv, false, nil
}

func writeAssistantReply(db *sql.DB, convID int64, text string, out io.Writer) error {
	clean, findings := redact.ScanAndRedact(text)
	if err := store.AddMessage(db, convID, "assistant", clean); err != nil {
		return err
	}
	if len(findings) > 0 {
		clean = "(Assistant response was redacted due to secret-like content.)\n" + clean
	}
	fmt.Fprintln(out, clean)
	return nil
}

func ensureTestUser(db *sql.DB, username, password string) (*store.User, error) {
	u, err := store.Authenticate(db, username, password)
	if err == nil {
		return u, nil
	}
	if err == store.ErrInvalidCredentials {
		created, createErr := store.CreateUser(db, username, password)
		if createErr == nil {
			return created, nil
		}
		if createErr != store.ErrUsernameTaken {
			return nil, createErr
		}
		return store.Authenticate(db, username, password)
	}
	return nil, err
}

func prepareTestConversation(cfg *config.Config, db *sql.DB, user *store.User) (*store.Conversation, error) {
	dirs := userspace.ForUser(cfg.Paths.DataDir, user.ID)
	if err := userspace.Ensure(dirs); err != nil {
		return nil, err
	}
	b, _ := yaml.Marshal(proactive.DefaultRules())
	_ = store.EnsureDefaultProactiveRulesYAML(db, user.ID, string(b))

	rulesPath := proactive.DefaultRulesPath(dirs.Config)
	if _, err := os.Stat(rulesPath); err != nil {
		if writeErr := os.WriteFile(rulesPath, b, 0o644); writeErr != nil {
			return nil, writeErr
		}
	}
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentChat)
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentProactiveDailyBrief)
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentProactiveOpenLoops)

	conv, err := store.GetOrCreateActiveConversation(db, user.ID)
	if err != nil {
		return nil, err
	}
	return conv, nil
}
