package signal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"

	"tether/internal/agent"
	"tether/internal/config"
	"tether/internal/redact"
	"tether/internal/store"
)

// Gateway integrates Signal via signal-cli daemon --http.
//
// It is intentionally minimal and expects signal-cli to be installed and the account to be registered.
type Gateway struct {
	cfg *config.Config
	db  *sql.DB
	ag  *agent.Agent

	http *http.Client

	mu  sync.Mutex
	cmd *exec.Cmd
}

func NewGateway(cfg *config.Config, db *sql.DB, ag *agent.Agent) *Gateway {
	return &Gateway{
		cfg:  cfg,
		db:   db,
		ag:   ag,
		http: &http.Client{Timeout: 0}, // SSE connection
	}
}

func (g *Gateway) Start(ctx context.Context) {
	if !g.cfg.Signal.Enabled {
		return
	}
	if strings.TrimSpace(g.cfg.Signal.AccountNumber) == "" {
		log.Warn("signal enabled but account_number missing")
		return
	}

	// Start signal-cli daemon.
	g.mu.Lock()
	if g.cmd == nil {
		addr := g.cfg.Signal.HTTPAddr
		g.cmd = exec.CommandContext(ctx, g.cfg.Signal.SignalCLIPath,
			"-a", g.cfg.Signal.AccountNumber,
			"daemon",
			"--http="+addr,
			"--no-receive-stdout",
		)
		// Keep stderr for diagnostics.
		var stderr bytes.Buffer
		g.cmd.Stderr = &stderr
		if err := g.cmd.Start(); err != nil {
			log.Error("failed to start signal-cli daemon", "error", err)
			g.cmd = nil
			g.mu.Unlock()
			return
		}
		log.Info("started signal-cli daemon", "http_addr", addr)
		// Log stderr asynchronously if it exits quickly.
		go func() {
			err := g.cmd.Wait()
			if err != nil && !errors.Is(err, context.Canceled) {
				log.Error("signal-cli daemon exited", "error", err, "stderr", stderr.String())
			}
		}()
	}
	g.mu.Unlock()

	// Wait for daemon check endpoint.
	if err := g.waitForCheck(ctx, 15*time.Second); err != nil {
		log.Warn("signal-cli daemon check failed", "error", err)
		return
	}

	go g.eventLoop(ctx)
}

func (g *Gateway) waitForCheck(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL()+"/api/v1/check", nil)
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("timeout")
}

func (g *Gateway) baseURL() string {
	addr := strings.TrimSpace(g.cfg.Signal.HTTPAddr)
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		return addr
	}
	return "http://" + addr
}

type receiveNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Envelope struct {
			Source       string `json:"source"`
			SourceNumber string `json:"sourceNumber"`
			DataMessage  struct {
				Message string `json:"message"`
			} `json:"dataMessage"`
		} `json:"envelope"`
		Account string `json:"account"`
	} `json:"params"`
}

func (g *Gateway) eventLoop(ctx context.Context) {
	backoff := 1 * time.Second
	lastPrune := time.Time{}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Periodically prune expired link codes.
		if lastPrune.IsZero() || time.Since(lastPrune) > 30*time.Minute {
			store.PruneSignalLinkCodes(g.db)
			lastPrune = time.Now()
		}

		err := g.consumeEvents(ctx)
		if err != nil {
			log.Warn("signal events loop error", "error", err)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func (g *Gateway) consumeEvents(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.baseURL()+"/api/v1/events", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("events status %d: %s", resp.StatusCode, string(b))
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" {
				continue
			}
			var n receiveNotification
			if err := json.Unmarshal([]byte(payload), &n); err != nil {
				continue
			}
			if n.Method != "receive" {
				continue
			}
			from := strings.TrimSpace(n.Params.Envelope.SourceNumber)
			if from == "" {
				from = strings.TrimSpace(n.Params.Envelope.Source)
			}
			msg := strings.TrimSpace(n.Params.Envelope.DataMessage.Message)
			if from == "" || msg == "" {
				continue
			}
			go g.handleInbound(ctx, from, msg)
		}
	}
	return scanner.Err()
}

func (g *Gateway) handleInbound(ctx context.Context, from string, text string) {
	// Best-effort audit without storing message content.
	sum := sha256.Sum256([]byte(text))
	payload, _ := json.Marshal(map[string]any{"from": from, "len": len(text), "sha256": hex.EncodeToString(sum[:])})
	_ = store.AddAuditEvent(g.db, nil, "signal_inbound", string(payload))

	// Linking: if message equals a pending code, link this number.
	uid, ok, err := store.ConsumeSignalLinkCode(g.db, text)
	if err == nil && ok {
		if err := store.LinkSignalNumber(g.db, uid, from); err == nil {
			_ = g.send(ctx, from, "Signal linked. You can now message Tether.")
			return
		}
	}

	uid, ok2, err := store.FindUserIDBySignalNumber(g.db, from)
	if err != nil || !ok2 {
		_ = g.send(ctx, from, "This number is not linked to a Tether user yet. Log into the SSH portal and run /signal link.")
		return
	}

	conv, err := store.GetOrCreateDefaultConversation(g.db, uid)
	if err != nil {
		_ = g.send(ctx, from, "Internal error.")
		return
	}

	clean, findings := redact.ScanAndRedact(text)
	_ = store.AddMessage(g.db, conv.ID, "user", clean)
	if len(findings) > 0 {
		_ = g.send(ctx, from, "Your message looked like it contained secrets/tokens and was redacted. Please use /secret add via SSH for secrets.")
	}

	reply, err := g.ag.Reply(ctx, agent.ReplyParams{UserID: uid, ConversationID: conv.ID, Text: clean})
	if err != nil {
		_ = g.send(ctx, from, "Agent error: "+err.Error())
		return
	}

	out, of := redact.ScanAndRedact(reply.Text)
	_ = store.AddMessage(g.db, conv.ID, "assistant", out)
	if len(of) > 0 {
		_ = g.send(ctx, from, "(Assistant response was redacted due to secret-like content.)\n"+out)
		return
	}
	_ = g.send(ctx, from, out)
}

func (g *Gateway) send(ctx context.Context, recipient string, message string) error {
	// Best-effort audit without storing message content.
	var uidPtr *int64
	if uid, ok, _ := store.FindUserIDBySignalNumber(g.db, recipient); ok {
		uidPtr = &uid
	}
	sum := sha256.Sum256([]byte(message))
	payload, _ := json.Marshal(map[string]any{"to": recipient, "len": len(message), "sha256": hex.EncodeToString(sum[:])})
	_ = store.AddAuditEvent(g.db, uidPtr, "signal_send", string(payload))

	id := fmt.Sprintf("tether-%d", time.Now().UnixNano())
	reqObj := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "send",
		"params": map[string]any{
			"recipient": []string{recipient},
			"message":   message,
		},
	}
	b, _ := json.Marshal(reqObj)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.baseURL()+"/api/v1/rpc", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("rpc status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}
