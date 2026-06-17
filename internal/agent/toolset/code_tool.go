package toolset

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"tether/internal/redact"
	"tether/internal/sandbox"
	"tether/internal/tools"
)

// Code runs a short Python script inside the user sandbox. The script can call
// the session's other enabled tools (and MCP server tools) over a local Unix
// socket, letting the model orchestrate and *filter* tool output in code rather
// than pulling large intermediate results back into the conversation.
//
// Execution model:
//   - The script runs in the same bubblewrap sandbox as `bash` (network off,
//     per-user root mounted at /work).
//   - A prelude is prepended to the script defining call_tool(), result() and
//     tools(). These talk to a host-side RPC loop over a pathname Unix socket
//     placed in the mounted work dir (pathname sockets are not affected by the
//     sandbox's network namespace).
//   - Each call_tool() reuses the normal tool dispatch path via Session.Tools,
//     so confirmation, auditing and redaction in the underlying tools still
//     apply. Tools that require confirmation cannot be driven from code mode
//     (the confirmation flow pauses the whole turn); such calls raise inside
//     the script and must be made directly instead.
//
// TODO(call-budget): code-mode tool calls are currently unbounded and do NOT
// count against the per-turn tool-call limit / justification gate that normal
// tool calls go through (see agent.replyWithToolsStream). A single script can
// therefore make an arbitrary number of tool calls. We count them and return
// the total, but do not yet enforce a cap. Decide on a budget (per-script
// limit and/or feeding the count back into the turn budget) before relying on
// this in untrusted contexts.
type Code struct{}

// codeRunTimeout bounds a single script run so a hung or looping script cannot
// stall the turn indefinitely.
const codeRunTimeout = 120 * time.Second

type codeArgs struct {
	Code string `json:"code"`
}

func (t Code) Spec() tools.ToolSpec {
	return tools.ToolSpec{
		Name: "code",
		Summary: "Run a short Python 3 script in the sandbox to orchestrate and filter your other enabled tools. " +
			"Use this instead of many separate tool calls when you would otherwise pull large intermediate results into the conversation.",
		WhenToUse: "Use when a task needs to chain several tool calls, loop over results, or distill a large tool output down to the few values you actually need. " +
			"Inside the script: call_tool(name, **kwargs) invokes any enabled tool and returns its result as Python data; " +
			"result(value) sets the JSON-serializable value returned to you; tools() lists the enabled tools. " +
			"Anything you print() is captured as stdout. Only the Python standard library is available and there is no network access from the script itself (the tools you call still run normally). " +
			"TOOL NAMES: call_tool accepts either the canonical dotted name (e.g. \"memory.list\", \"tool.search\") or the underscore form you see in the tool list (\"memory_list\", \"tool_search\") — both resolve. " +
			"A tool must already be enabled: call tools() first to see exactly what is callable; a \"tool not enabled\" error means you must enable that tool before the script can use it, not that the name was wrong.",
		Safety: "Runs in the same sandbox as bash (network off, /work mounted). Tools that require user confirmation cannot be called from code mode and will raise; call those directly instead.",
		InputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"code": map[string]any{"type": "string", "minLength": 1, "description": "Python 3 source to execute"},
			},
			"required": []string{"code"},
		},
		OutputSchema: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"result":           map[string]any{"description": "value passed to result(), if any"},
				"stdout":           map[string]any{"type": "string"},
				"stderr":           map[string]any{"type": "string"},
				"exit_code":        map[string]any{"type": "integer"},
				"stdout_truncated": map[string]any{"type": "boolean"},
				"stderr_truncated": map[string]any{"type": "boolean"},
				"tool_calls":       map[string]any{"type": "integer", "description": "number of call_tool() invocations made"},
			},
			"required": []string{"stdout", "stderr", "exit_code", "tool_calls"},
		},
		Examples: []tools.ToolExample{
			{
				Title: "Filter a large tool result",
				Args: map[string]any{"code": "res = call_tool(\"web-fetch\", url=\"https://example.com\")\n" +
					"# keep only what we need instead of returning the whole page\n" +
					"result({\"title\": res.get(\"title\"), \"len\": len(res.get(\"text\", \"\"))})"},
				Notes: "result() sets the returned value; nothing large needs to enter the conversation.",
			},
		},
		Tags: []string{"python", "sandbox", "orchestration"},
	}
}

func (t Code) Definition() ToolDef {
	spec := t.Spec()
	return ToolDef{Name: spec.Name, Description: tools.LLMDescription(spec), Parameters: spec.InputSchema}
}

func (t Code) Execute(ctx context.Context, s *Session, rawArgs json.RawMessage) (any, error) {
	var args codeArgs
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return nil, err
	}
	code := strings.TrimSpace(args.Code)
	if code == "" {
		return nil, fmt.Errorf("code required")
	}
	if s == nil || s.Tools == nil {
		return nil, fmt.Errorf("code tool not configured")
	}
	if strings.TrimSpace(s.Dirs.Root) == "" {
		return nil, fmt.Errorf("code tool requires a sandbox work dir")
	}

	// Per-run scratch dir inside the mounted work root. Keep the name short so
	// the resulting Unix socket path stays under the ~108 byte limit.
	suffix, err := randHex(4)
	if err != nil {
		return nil, err
	}
	dirName := ".tc-" + suffix
	hostDir := filepath.Join(s.Dirs.Root, dirName)
	if err := os.MkdirAll(hostDir, 0o700); err != nil {
		return nil, fmt.Errorf("create scratch dir: %w", err)
	}
	defer os.RemoveAll(hostDir)

	hostSock := filepath.Join(hostDir, "s")
	sandboxSock := "/work/" + dirName + "/s"
	sandboxScript := "/work/" + dirName + "/script.py"

	script := codePrelude(sandboxSock, t.activeToolSummaries(s)) + "\n# --- begin user code ---\n" + code + "\n"
	if err := os.WriteFile(filepath.Join(hostDir, "script.py"), []byte(script), 0o600); err != nil {
		return nil, fmt.Errorf("write script: %w", err)
	}

	ln, err := net.Listen("unix", hostSock)
	if err != nil {
		return nil, fmt.Errorf("create rpc socket: %w", err)
	}
	defer ln.Close()

	srv := &codeRPCServer{session: s, invoker: s.Tools}
	go srv.serve(ctx, ln)

	runCtx, cancel := context.WithTimeout(ctx, codeRunTimeout)
	defer cancel()

	res, err := sandbox.Run(runCtx, s.Dirs.Root, []string{"python3", sandboxScript}, true)
	// Stop accepting RPCs once the script has exited.
	_ = ln.Close()
	if err != nil {
		return nil, err
	}

	stdout, _ := redact.ScanAndRedact(res.Stdout)
	stderr, _ := redact.ScanAndRedact(res.Stderr)

	out := map[string]any{
		"stdout":           stdout,
		"stderr":           stderr,
		"exit_code":        res.ExitCode,
		"stdout_truncated": res.StdoutTruncated,
		"stderr_truncated": res.StderrTruncated,
		"tool_calls":       srv.callCount(),
	}
	if v, ok := srv.result(); ok {
		out["result"] = v
	}
	return out, nil
}

// activeToolSummaries returns the canonical names and summaries of the tools
// currently enabled in the session, excluding `code` itself. This is embedded
// into the script as the tools() table so the model can introspect what is
// callable without bloating the prompt.
func (t Code) activeToolSummaries(s *Session) map[string]string {
	out := map[string]string{}
	if s == nil {
		return out
	}
	for name, on := range s.Active {
		if !on || name == "code" {
			continue
		}
		summary := ""
		if s.Registry != nil {
			if spec, ok := s.Registry.Get(name); ok {
				summary = spec.Summary
			}
		}
		out[name] = summary
	}
	return out
}

// codeRPCServer handles the line-delimited JSON protocol spoken by the script
// prelude over the Unix socket.
type codeRPCServer struct {
	session *Session
	invoker ToolInvoker

	mu        sync.Mutex
	calls     int
	resultVal json.RawMessage
	resultSet bool
}

func (srv *codeRPCServer) callCount() int {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.calls
}

func (srv *codeRPCServer) result() (json.RawMessage, bool) {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	return srv.resultVal, srv.resultSet
}

func (srv *codeRPCServer) serve(ctx context.Context, ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go srv.handle(ctx, conn)
	}
}

type codeRPCRequest struct {
	Op    string          `json:"op"`
	ID    int             `json:"id"`
	Name  string          `json:"name"`
	Args  json.RawMessage `json:"args"`
	Value json.RawMessage `json:"value"`
}

func (srv *codeRPCServer) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReaderSize(conn, 1<<20)
	w := bufio.NewWriter(conn)
	for {
		line, err := r.ReadBytes('\n')
		if err != nil {
			return
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}
		var req codeRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			srv.writeJSON(w, map[string]any{"ok": false, "error": "malformed request"})
			continue
		}
		switch req.Op {
		case "call":
			srv.mu.Lock()
			srv.calls++
			srv.mu.Unlock()
			rawArgs := req.Args
			if len(strings.TrimSpace(string(rawArgs))) == 0 {
				rawArgs = json.RawMessage("{}")
			}
			result, callErr := srv.invoker.Invoke(ctx, srv.session, req.Name, rawArgs)
			if callErr != nil {
				srv.writeJSON(w, map[string]any{"id": req.ID, "ok": false, "error": callErr.Error()})
				continue
			}
			srv.writeJSON(w, map[string]any{"id": req.ID, "ok": true, "result": result})
		case "result":
			srv.mu.Lock()
			srv.resultVal = append(json.RawMessage(nil), req.Value...)
			srv.resultSet = true
			srv.mu.Unlock()
			srv.writeJSON(w, map[string]any{"ok": true})
		default:
			srv.writeJSON(w, map[string]any{"id": req.ID, "ok": false, "error": "unknown op: " + req.Op})
		}
	}
}

func (srv *codeRPCServer) writeJSON(w *bufio.Writer, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte(`{"ok":false,"error":"host marshal error"}`)
	}
	_, _ = w.Write(b)
	_ = w.WriteByte('\n')
	_ = w.Flush()
}

func randHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// codePrelude builds the Python prelude prepended to user code. It opens the
// RPC connection and defines the call_tool/result/tools helpers.
func codePrelude(sockPath string, toolSummaries map[string]string) string {
	sockJSON, _ := json.Marshal(sockPath)

	// Stable ordering keeps generated scripts deterministic.
	names := make([]string, 0, len(toolSummaries))
	for name := range toolSummaries {
		names = append(names, name)
	}
	sort.Strings(names)
	ordered := make(map[string]string, len(names))
	for _, name := range names {
		ordered[name] = toolSummaries[name]
	}
	toolsJSON, _ := json.Marshal(ordered)

	return fmt.Sprintf(`# Tether code mode.
# Helpers: call_tool(name, **kwargs), result(value), tools().
# Tool names: call_tool accepts BOTH the canonical dotted name (e.g.
# "memory.list", "tool.search") and the underscore form ("memory_list",
# "tool_search") -- both resolve to the same tool. Only enabled tools are
# callable; the keys of TOOLS (and tools()) are exactly what is enabled now.
import socket as _socket, json as _json

_TETHER_SOCK = %s
TOOLS = _json.loads(%s)


class ToolError(Exception):
    pass


class _TetherClient:
    def __init__(self, path):
        s = _socket.socket(_socket.AF_UNIX, _socket.SOCK_STREAM)
        s.connect(path)
        self._f = s.makefile("rwb")
        self._id = 0

    def _send(self, msg):
        self._f.write((_json.dumps(msg) + "\n").encode("utf-8"))
        self._f.flush()
        line = self._f.readline()
        if not line:
            raise ToolError("tether: host closed the connection")
        return _json.loads(line.decode("utf-8"))

    def call(self, name, args):
        self._id += 1
        r = self._send({"op": "call", "id": self._id, "name": name, "args": args})
        if not r.get("ok"):
            raise ToolError(r.get("error") or ("tool call failed: " + str(name)))
        return r.get("result")

    def result(self, value):
        self._send({"op": "result", "value": value})


_tether = _TetherClient(_TETHER_SOCK)


def call_tool(name, **kwargs):
    """Invoke an enabled tool; returns its result as Python data.

    name accepts either the canonical dotted form ("memory.list") or the
    underscore form ("memory_list"). See tools() for what is enabled.
    """
    return _tether.call(name, kwargs)


def result(value):
    """Set the JSON-serializable value returned to the model."""
    _tether.result(value)


def tools():
    """Return {name: summary} for the tools enabled in this session."""
    return dict(TOOLS)
`, string(sockJSON), mustJSONStringLiteral(toolsJSON))
}

// mustJSONStringLiteral renders a byte slice as a Python string literal via JSON
// encoding (JSON string escaping is a subset of valid Python string escaping).
func mustJSONStringLiteral(b []byte) string {
	lit, _ := json.Marshal(string(b))
	return string(lit)
}
