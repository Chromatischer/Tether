package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"charm.land/log/v2"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"tether/internal/config"
)

// Manager manages configured MCP server connections and tool discovery.
//
// For now we support only the MCP stdio transport via CommandTransport.
//
// Notes:
// - Server configs are global (from tether.yaml).
// - Tool availability is constrained per user elsewhere (session.Allowed).
// - We keep per-user sessions per server to avoid cross-user state leakage.
//
// Future:
// - SSE/HTTP transports
// - listChanged notifications
// - per-user server configs/credentials
// - output caching for large/binary results
//
// The manager is safe for concurrent use.
type Manager struct {
	mu sync.Mutex

	cfg *config.Config

	client *mcpsdk.Client

	servers map[string]*serverState

	// cached discovery state (best-effort)
	lastRefresh time.Time
}

type serverState struct {
	cfg config.MCPServer

	// tool definitions as last discovered from the server
	tools     []*mcpsdk.Tool
	lastErr   error
	refreshed time.Time

	sessions map[int64]*userSession // userID -> session
}

type userSession struct {
	sess     *mcpsdk.ClientSession
	lastUsed time.Time
}

type ServerInfo struct {
	Name                      string
	Trusted                   bool
	DefaultEnabledForAllUsers bool
	Transport                 string
	Command                   string
	Args                      []string
	ToolsN                    int
	LastRefreshUTC            string
	LastError                 string
}

func NewManager(cfg *config.Config) *Manager {
	impl := &mcpsdk.Implementation{Name: "tether", Version: "0.4"}
	c := mcpsdk.NewClient(impl, nil)
	m := &Manager{cfg: cfg, client: c, servers: map[string]*serverState{}}
	m.reloadServersLocked()
	return m
}

func (m *Manager) ReloadConfig(cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.reloadServersLocked()
}

func (m *Manager) reloadServersLocked() {
	m.servers = map[string]*serverState{}
	if m.cfg == nil {
		return
	}
	if m.cfg.MCP.Enabled != nil && !*m.cfg.MCP.Enabled {
		return
	}
	for _, sc := range m.cfg.MCP.Servers {
		name := strings.TrimSpace(sc.Name)
		if name == "" {
			continue
		}
		sc.Name = name
		if strings.TrimSpace(sc.Transport) == "" {
			sc.Transport = "stdio"
		}
		m.servers[name] = &serverState{cfg: sc, sessions: map[int64]*userSession{}}
	}
}

func (m *Manager) DefaultEnabledServers() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]bool{}
	for name, st := range m.servers {
		if st != nil && st.cfg.DefaultEnabledForAllUsers {
			out[name] = true
		}
	}
	return out
}

// Refresh discovers tools for all configured servers and caches results.
// It does not keep discovery sessions open.
func (m *Manager) Refresh(ctx context.Context) {
	m.mu.Lock()
	servers := make([]*serverState, 0, len(m.servers))
	for _, st := range m.servers {
		if st != nil {
			servers = append(servers, st)
		}
	}
	m.mu.Unlock()

	for _, st := range servers {
		tools, err := m.listToolsOnce(ctx, st.cfg)
		m.mu.Lock()
		if current := m.servers[st.cfg.Name]; current != nil {
			current.tools = tools
			current.lastErr = err
			current.refreshed = time.Now().UTC()
		}
		m.lastRefresh = time.Now().UTC()
		m.mu.Unlock()

		if err != nil {
			log.Warn("mcp tool discovery failed", "server", st.cfg.Name, "error", err)
		}
	}
}

func (m *Manager) Infos() []ServerInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ServerInfo, 0, len(m.servers))
	for name, st := range m.servers {
		if st == nil {
			continue
		}
		info := ServerInfo{
			Name:                      name,
			Trusted:                   st.cfg.Trusted,
			DefaultEnabledForAllUsers: st.cfg.DefaultEnabledForAllUsers,
			Transport:                 st.cfg.Transport,
			Command:                   st.cfg.Command,
			Args:                      append([]string(nil), st.cfg.Args...),
			ToolsN:                    len(st.tools),
		}
		if !st.refreshed.IsZero() {
			info.LastRefreshUTC = st.refreshed.UTC().Format(time.RFC3339)
		}
		if st.lastErr != nil {
			info.LastError = st.lastErr.Error()
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (m *Manager) ToolsForServer(serverName string) ([]*mcpsdk.Tool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.servers[strings.TrimSpace(serverName)]
	if st == nil {
		return nil, false
	}
	return append([]*mcpsdk.Tool(nil), st.tools...), true
}

func (m *Manager) ServerConfig(serverName string) (config.MCPServer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.servers[strings.TrimSpace(serverName)]
	if st == nil {
		return config.MCPServer{}, false
	}
	return st.cfg, true
}

// CallTool calls a tool on the given MCP server, using a per-user session.
func (m *Manager) CallTool(ctx context.Context, userID int64, serverName string, toolName string, arguments map[string]any) (*mcpsdk.CallToolResult, error) {
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	if serverName == "" || toolName == "" {
		return nil, fmt.Errorf("mcp: server and tool name required")
	}

	// Best-effort prune idle sessions.
	m.pruneIdleLocked(time.Now().UTC())

	sess, err := m.getOrConnectSession(ctx, userID, serverName)
	if err != nil {
		return nil, err
	}

	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: arguments})
	if err == nil {
		m.touchSession(userID, serverName)
		return res, nil
	}

	// Retry once on connection-closed errors.
	if errors.Is(err, mcpsdk.ErrConnectionClosed) {
		_ = m.closeSession(userID, serverName)
		sess2, err2 := m.getOrConnectSession(ctx, userID, serverName)
		if err2 != nil {
			return nil, err
		}
		res2, err2 := sess2.CallTool(ctx, &mcpsdk.CallToolParams{Name: toolName, Arguments: arguments})
		if err2 == nil {
			m.touchSession(userID, serverName)
			return res2, nil
		}
		return nil, err2
	}
	return nil, err
}

// ConfirmScope returns a confirmation scope for the given tool call.
// It is stable for a given (server, tool, args) triple.
func ConfirmScope(serverName, toolName string, args map[string]any) string {
	serverName = strings.TrimSpace(serverName)
	toolName = strings.TrimSpace(toolName)
	cleanArgs := map[string]any{}
	for k, v := range args {
		if strings.EqualFold(k, "confirm_token") {
			continue
		}
		cleanArgs[k] = v
	}
	b := mustStableJSON(cleanArgs)
	sum := sha256.Sum256(b)
	h := hex.EncodeToString(sum[:8])
	return fmt.Sprintf("mcp:%s:%s:%s", serverName, toolName, h)
}

func mustStableJSON(v any) []byte {
	// encoding/json sorts map keys, so this is stable enough for scope hashing.
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return b
}

func (m *Manager) pruneIdleLocked(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	idle := 10 * time.Minute
	for _, st := range m.servers {
		if st == nil {
			continue
		}
		for uid, us := range st.sessions {
			if us == nil || us.sess == nil {
				delete(st.sessions, uid)
				continue
			}
			if now.Sub(us.lastUsed) > idle {
				_ = us.sess.Close()
				delete(st.sessions, uid)
			}
		}
	}
}

func (m *Manager) touchSession(userID int64, serverName string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.servers[serverName]
	if st == nil {
		return
	}
	us := st.sessions[userID]
	if us == nil {
		return
	}
	us.lastUsed = time.Now().UTC()
}

func (m *Manager) getOrConnectSession(ctx context.Context, userID int64, serverName string) (*mcpsdk.ClientSession, error) {
	m.mu.Lock()
	st := m.servers[serverName]
	if st == nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("mcp: unknown server: %s", serverName)
	}
	if us := st.sessions[userID]; us != nil && us.sess != nil {
		us.lastUsed = time.Now().UTC()
		s := us.sess
		m.mu.Unlock()
		return s, nil
	}
	cfg := st.cfg
	m.mu.Unlock()

	sess, err := m.connect(ctx, cfg)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	st2 := m.servers[serverName]
	if st2 == nil {
		m.mu.Unlock()
		_ = sess.Close()
		return nil, fmt.Errorf("mcp: server removed: %s", serverName)
	}
	st2.sessions[userID] = &userSession{sess: sess, lastUsed: time.Now().UTC()}
	m.mu.Unlock()
	return sess, nil
}

func (m *Manager) closeSession(userID int64, serverName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := m.servers[serverName]
	if st == nil {
		return nil
	}
	us := st.sessions[userID]
	if us == nil || us.sess == nil {
		delete(st.sessions, userID)
		return nil
	}
	err := us.sess.Close()
	delete(st.sessions, userID)
	return err
}

func (m *Manager) connect(ctx context.Context, cfg config.MCPServer) (*mcpsdk.ClientSession, error) {
	if strings.TrimSpace(cfg.Transport) == "" {
		cfg.Transport = "stdio"
	}
	if cfg.Transport != "stdio" {
		return nil, fmt.Errorf("mcp: unsupported transport %q for server %q", cfg.Transport, cfg.Name)
	}
	cmdName := strings.TrimSpace(cfg.Command)
	if cmdName == "" {
		return nil, fmt.Errorf("mcp: server %q missing command", cfg.Name)
	}
	cmd := exec.CommandContext(ctx, cmdName, cfg.Args...)
	cmd.Stderr = os.Stderr
	cmd.Env = mergedEnv(os.Environ(), cfg.Env)
	transport := &mcpsdk.CommandTransport{Command: cmd}
	return m.client.Connect(ctx, transport, nil)
}

func (m *Manager) listToolsOnce(ctx context.Context, cfg config.MCPServer) ([]*mcpsdk.Tool, error) {
	// Use a short-lived connection for discovery.
	ctx2, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	sess, err := m.connect(ctx2, cfg)
	if err != nil {
		return nil, err
	}
	defer sess.Close()

	tools := []*mcpsdk.Tool{}
	params := &mcpsdk.ListToolsParams{}
	for {
		res, err := sess.ListTools(ctx2, params)
		if err != nil {
			return nil, err
		}
		tools = append(tools, res.Tools...)
		if strings.TrimSpace(res.NextCursor) == "" {
			break
		}
		params.Cursor = res.NextCursor
	}
	return tools, nil
}

func mergedEnv(base []string, overrides map[string]string) []string {
	if len(overrides) == 0 {
		return base
	}
	m := map[string]string{}
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m[k] = v
	}
	for k, v := range overrides {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		m[k] = expandEnvValue(strings.TrimSpace(v))
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
}

func expandEnvValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "${env:") && strings.HasSuffix(v, "}") {
		key := strings.TrimSuffix(strings.TrimPrefix(v, "${env:"), "}")
		key = strings.TrimSpace(key)
		if key == "" {
			return ""
		}
		return os.Getenv(key)
	}
	return v
}
