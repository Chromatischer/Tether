package term

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"
	"gopkg.in/yaml.v3"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/personality"
	"tether/internal/proactive"
	"tether/internal/redact"
	"tether/internal/store"
	"tether/internal/userspace"
)

const (
	historyLines = 20
)

func Run(ctx context.Context, cfg *config.Config, database *sql.DB, ag *agent.Agent, continueConv bool, textMode bool) error {
	profile := DetectLocalTerminalProfile(textMode)
	profile = setTerminalSize(profile)

	DisplayLogo(profile)

	user, err := login(database, profile)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	dirs := userspace.ForUser(cfg.Paths.DataDir, user.ID)
	if err := userspace.Ensure(dirs); err != nil {
		return fmt.Errorf("failed to ensure user directories: %w", err)
	}

	if err := seedPersonalities(dirs, database, user.ID); err != nil {
		PrintSystem(profile, "Warning: failed to seed personalities: "+err.Error())
	}

	var conv *store.Conversation
	if continueConv {
		conv, err = store.GetOrCreateActiveConversation(database, user.ID)
		if err != nil {
			return fmt.Errorf("failed to load conversation: %w", err)
		}
	} else {
		conv, err = store.CreateConversation(database, user.ID, "")
		if err != nil {
			return fmt.Errorf("failed to create conversation: %w", err)
		}
		if err := store.SetActiveConversation(database, user.ID, conv.ID); err != nil {
			return fmt.Errorf("failed to set active conversation: %w", err)
		}
	}

	DisplayWelcome(profile, user.Username)

	if continueConv {
		messages, err := store.ListRecentMessages(database, conv.ID, historyLines)
		if err == nil {
			for _, msg := range messages {
				printHistoryMessage(msg, profile)
			}
		}
		fmt.Println()
	}

	editor, err := newLineEditor(profile)
	if err != nil {
		return fmt.Errorf("failed to initialize input: %w", err)
	}
	defer editor.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	go func() {
		select {
		case <-sigCh:
			cancelRun()
		case <-runCtx.Done():
		}
	}()

	replLoop(runCtx, editor, profile, ag, database, user.ID, conv.ID)

	st := ag.SessionStatus(user.ID, conv.ID)
	PrintExitSummary(profile, conv.ID, st.TotalToolCalls, st.TotalInputTokens, st.TotalOutputTokens, st.TotalCost)
	return nil
}

func setTerminalSize(profile TerminalProfile) TerminalProfile {
	fd := int(os.Stdout.Fd())
	if w, _, err := term.GetSize(fd); err == nil {
		profile.Width = w
	} else {
		profile.Width = 80
	}

	if _, h, err := term.GetSize(fd); err == nil {
		profile.Height = h
	} else {
		profile.Height = 24
	}

	return profile
}

func login(database *sql.DB, profile TerminalProfile) (*store.User, error) {
	var username string
	if !profile.TextMode && profile.IsTTY {
		fmt.Print("Username: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			username = strings.TrimSpace(scanner.Text())
		}
	} else {
		fmt.Print("Username: ")
		scanner := bufio.NewScanner(os.Stdin)
		if scanner.Scan() {
			username = strings.TrimSpace(scanner.Text())
		}
	}

	if username == "" {
		return nil, fmt.Errorf("username required")
	}

	user, err := store.Authenticate(database, username, "")
	if err == nil {
		password, pErr := readPassword("Password: ")
		if pErr != nil {
			return nil, fmt.Errorf("password read error: %w", pErr)
		}
		user, err = store.Authenticate(database, username, password)
		if err != nil {
			return nil, err
		}
		fmt.Println()
		return user, nil
	}

	if err != store.ErrInvalidCredentials {
		fmt.Println()
		fmt.Printf("New user. Creating account for '%s'.\n", username)
		password, pErr := readPassword("Password: ")
		if pErr != nil {
			return nil, fmt.Errorf("password read error: %w", pErr)
		}
		password2, pErr := readPassword("Confirm: ")
		if pErr != nil {
			return nil, fmt.Errorf("password read error: %w", pErr)
		}
		if password != password2 {
			return nil, fmt.Errorf("passwords do not match")
		}
		user, err = store.CreateUser(database, username, password)
		if err != nil {
			return nil, err
		}
		fmt.Println()
		return user, nil
	}

	password, pErr := readPassword("Password: ")
	if pErr != nil {
		return nil, fmt.Errorf("password read error: %w", pErr)
	}

	user, err = store.Authenticate(database, username, password)
	if err != nil {
		return nil, err
	}
	fmt.Println()
	return user, nil
}

func readPassword(prompt string) (string, error) {
	fmt.Print(prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(pw), err
}

func formatMsgTime(ts string) string {
	for _, layout := range []string{
		"2006-01-02T15:04:05.999999Z",
		"2006-01-02T15:04:05.999999-07:00",
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04:05.000000",
	} {
		if t, err := time.Parse(layout, ts); err == nil {
			return t.Format("15:04")
		}
	}
	return ts
}

func seedPersonalities(dirs userspace.Dirs, database *sql.DB, userID int64) error {
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentChat)
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentProactiveDailyBrief)
	_ = userspace.EnsurePersonalityFile(dirs, personality.AgentProactiveOpenLoops)

	b, _ := yaml.Marshal(proactive.DefaultRules())
	_ = store.EnsureDefaultProactiveRulesYAML(database, userID, string(b))

	rulesPath := proactive.DefaultRulesPath(dirs.Config)
	if _, err := os.Stat(rulesPath); err != nil {
		_ = os.WriteFile(rulesPath, b, 0o644)
	}
	if rules, err := proactive.LoadRules(rulesPath); err == nil {
		for _, ar := range rules.Agents {
			if id, ok := personality.NormalizeID(ar.ID); ok {
				_ = userspace.EnsurePersonalityFile(dirs, personality.ProactiveAgentKey(id))
			}
		}
	}
	return nil
}

func printHistoryMessage(msg store.Message, profile TerminalProfile) {
	timestamp := formatMsgTime(msg.CreatedAt)
	switch msg.Role {
	case "user":
		fmt.Print(apply(newPalette(profile.ColorLevel).brightWhite(), bold("You")+" "))
		fmt.Print(apply(newPalette(profile.ColorLevel).muted(), timestamp))
		fmt.Println()
		fmt.Println(msg.Content)
	case "assistant":
		fmt.Print(apply(newPalette(profile.ColorLevel).cyan(), bold("Agent")+" "))
		fmt.Print(apply(newPalette(profile.ColorLevel).muted(), timestamp))
		fmt.Println()
		if profile.TextMode || profile.ColorLevel == ColorNone {
			fmt.Println(msg.Content)
		} else {
			rendered := renderMarkdown(msg.Content, profile.Width-4, profile)
			fmt.Println(rendered)
		}
	case "system", "notice":
		PrintSystem(profile, msg.Content)
	}
	fmt.Println()
}

func replLoop(ctx context.Context, editor *lineEditor, profile TerminalProfile, ag *agent.Agent, database *sql.DB, userID, convID int64) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		ShowPrompt(profile)
		input, err := editor.ReadLine()
		if err != nil {
			if err.Error() == "EOF" || err.Error() == "interrupted" {
				return
			}
			PrintError(profile, "Input error: "+err.Error())
			continue
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/") {
			if handled := handleCommand(input, profile, ag, database, editor, userID, &convID); handled {
				continue
			}
		}

		if err := processTurn(ctx, editor, profile, ag, database, userID, convID, input); err != nil {
			if ctx.Err() != nil {
				return
			}
			PrintError(profile, err.Error())
		}
	}
}

func handleCommand(input string, profile TerminalProfile, ag *agent.Agent, database *sql.DB, editor *lineEditor, userID int64, convID *int64) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return false
	}
	cmd := strings.ToLower(parts[0])

	switch cmd {
	case "/quit", "/exit":
		fmt.Println()
		return true

	case "/help":
		printHelp(profile)
		return true

	case "/clear":
		fmt.Print("\033[2J\033[H")
		DisplayLogo(profile)
		return true

	case "/confirm":
		if len(parts) < 2 {
			PrintSystem(profile, "Usage: /confirm <token>")
			return true
		}
		token := parts[1]
		if ag.HasPendingConfirmation(userID, *convID) {
			PrintSystem(profile, "Resuming with confirmed token: "+token)
			if err := processConfirmTurn(ag, database, editor, profile, userID, *convID, token); err != nil {
				PrintError(profile, err.Error())
			}
		} else {
			PrintSystem(profile, "No pending confirmation for this conversation.")
		}
		return true

	case "/reject":
		if len(parts) < 2 {
			PrintSystem(profile, "Usage: /reject <token>")
			return true
		}
		token := parts[1]
		if ag.HasPendingConfirmation(userID, *convID) {
			ag.RejectConfirmToken(userID, token)
			PrintSystem(profile, "Confirmation rejected.")
		} else {
			PrintSystem(profile, "No pending confirmation for this conversation.")
		}
		return true

	case "/new":
		newConv, err := store.CreateConversation(database, userID, "")
		if err != nil {
			PrintError(profile, "Failed to create conversation: "+err.Error())
			return true
		}
		if err := store.SetActiveConversation(database, userID, newConv.ID); err != nil {
			PrintError(profile, "Failed to set active conversation: "+err.Error())
			return true
		}
		*convID = newConv.ID
		PrintSystem(profile, fmt.Sprintf("New conversation created (ID: %d).", newConv.ID))
		return true

	default:
		return false
	}
}

func printHelp(profile TerminalProfile) {
	pal := newPalette(profile.ColorLevel)
	fmt.Println()
	fmt.Println(apply(pal.brightWhite(), bold("Tether — Terminal Mode")))
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Printf("  %s  Exit the application\n", apply(pal.yellow(), "/quit, /exit"))
	fmt.Printf("  %s    Show this help\n", apply(pal.yellow(), "/help"))
	fmt.Printf("  %s    Clear the screen\n", apply(pal.yellow(), "/clear"))
	fmt.Printf("  %s    Create a new conversation\n", apply(pal.yellow(), "/new"))
	fmt.Printf("  %s Confirm a pending action\n", apply(pal.yellow(), "/confirm <token>"))
	fmt.Printf("  %s  Reject a pending action\n", apply(pal.yellow(), "/reject <token>"))
	fmt.Println()
	fmt.Println("Input: Alt+Enter inserts a newline. Enter sends the message.")
	fmt.Println()
}

func processConfirmTurn(ag *agent.Agent, database *sql.DB, editor *lineEditor, profile TerminalProfile, userID, convID int64, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	state := newStreamState(profile.Width)
	reply, _, resumed, err := ag.ResumeConfirmedStream(ctx, userID, token, func(ev agent.StreamEvent) {
		handleStreamEvent(ev, profile, state)
	})
	if err != nil {
		return err
	}
	if !resumed {
		PrintSystem(profile, "Confirmation token not found or already used.")
		return nil
	}

	fmt.Println()
	if reply.Text != "" {
		clean, _ := redact.ScanAndRedact(reply.Text)
		_ = store.AddMessage(database, convID, "assistant", clean)
	}
	return nil
}

func processTurn(ctx context.Context, editor *lineEditor, profile TerminalProfile, ag *agent.Agent, database *sql.DB, userID, convID int64, text string) error {
	clean, _ := redact.ScanAndRedact(text)
	if err := store.AddMessage(database, convID, "user", clean); err != nil {
		return fmt.Errorf("failed to save message: %w", err)
	}

	fmt.Println()

	editor.LeaveRawMode()
	defer editor.EnterRawMode()

	streamCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	state := newStreamState(profile.Width)
	reply, err := ag.ReplyStream(streamCtx, agent.ReplyParams{
		UserID:         userID,
		ConversationID: convID,
		Text:           text,
	}, func(ev agent.StreamEvent) {
		handleStreamEvent(ev, profile, state)
	})

	if err != nil {
		return fmt.Errorf("agent error: %w", err)
	}

	if reply.Text != "" {
		cleanReply, _ := redact.ScanAndRedact(reply.Text)
		_ = store.AddMessage(database, convID, "assistant", cleanReply)
	}

	fmt.Println()
	return nil
}

func handleStreamEvent(ev agent.StreamEvent, profile TerminalProfile, state *streamState) {
	switch ev.Type {
	case "assistant_delta":
		if profile.ColorLevel == ColorNone || !profile.IsTTY {
			fmt.Print(ev.Delta)
			state.atLineStart = false
		} else {
			state.onAssistantDelta(profile, ev.Delta)
		}

	case "reasoning_delta":
		if profile.TextMode {
			return
		}
		state.onReasoningDelta(profile, ev.Delta)

	case "tool_call":
		if profile.TextMode {
			fmt.Printf("\n  [tool] %s(%s)\n", ev.Tool.Name, ev.Tool.Args)
		} else {
			state.onToolCall(profile, ev.Tool.Name, ev.Tool.Args)
		}

	case "tool_result":
		result := ev.Tool.Result
		if result == "" {
			result = ev.Err
		}
		if profile.TextMode {
			if result != "" {
				fmt.Printf("    -> %s\n", result)
			}
		} else {
			state.onToolResult(profile, result)
		}

	case "error":
		if profile.TextMode {
			fmt.Printf("\n[error] %s\n", ev.Err)
		} else {
			state.onError(profile, ev.Err)
		}

	case "done":
	}
}
