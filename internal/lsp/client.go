package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ResponseError is a JSON-RPC error.
type ResponseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *ResponseError) Error() string { return fmt.Sprintf("lsp: %s (code %d)", e.Message, e.Code) }

type envelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *ResponseError  `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    error
}

// Client is a JSON-RPC 2.0 client over a language-server stdio transport.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser

	writeMu sync.Mutex
	nextID  atomic.Int64
	pendMu  sync.Mutex
	pending map[int64]chan reply

	notify func(method string, params json.RawMessage)
	done   chan struct{}
	once   sync.Once
	err    error
	logs   *LogRing
}

// LogRing retains the most recent server log lines for diagnostics.
type LogRing struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func NewLogRing(max int) *LogRing { return &LogRing{max: max} }

func (l *LogRing) Add(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	s = strings.TrimRight(s, "\r\n")
	if s == "" {
		return
	}
	l.lines = append(l.lines, s)
	if len(l.lines) > l.max {
		l.lines = l.lines[len(l.lines)-l.max:]
	}
}

func (l *LogRing) Tail(n int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if n <= 0 || n > len(l.lines) {
		n = len(l.lines)
	}
	out := make([]string, n)
	copy(out, l.lines[len(l.lines)-n:])
	return out
}

// StartClient launches command and starts the reader loop. notify receives
// server notifications and must not block.
func StartClient(command string, args []string, env []string, workdir string, notify func(string, json.RawMessage)) (*Client, error) {
	cmd := exec.Command(command, args...)
	cmd.Env = mergeEnv(env)
	if workdir != "" {
		cmd.Dir = workdir
	}
	ring := NewLogRing(300)
	cmd.Stderr = &ringWriter{ring: ring}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c := &Client{
		cmd:     cmd,
		stdin:   stdin,
		stdout:  stdout,
		pending: make(map[int64]chan reply),
		notify:  notify,
		done:    make(chan struct{}),
		logs:    ring,
	}
	go c.readLoop()
	return c, nil
}

func mergeEnv(extra []string) []string {
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	keys := make(map[string]bool, len(extra))
	for _, e := range extra {
		if i := strings.IndexByte(e, '='); i > 0 {
			keys[e[:i]] = true
		}
	}
	out := make([]string, 0, len(env)+len(extra))
	for _, e := range env {
		if i := strings.IndexByte(e, '='); i > 0 && keys[e[:i]] {
			continue
		}
		out = append(out, e)
	}
	return append(out, extra...)
}

type ringWriter struct{ ring *LogRing }

func (w *ringWriter) Write(p []byte) (int, error) {
	w.ring.Add(string(p))
	return len(p), nil
}

// Logs returns recent server stderr output.
func (c *Client) Logs(n int) []string { return c.logs.Tail(n) }

// AddLog appends a client-side log line (for window/logMessage).
func (c *Client) AddLog(s string) { c.logs.Add(s) }

// Done is closed when the transport terminates.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err returns the terminal transport error, if any.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Call sends a request and waits for its response.
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, err
	}
	ch := make(chan reply, 1)
	c.pendMu.Lock()
	c.pending[id] = ch
	c.pendMu.Unlock()
	if err := c.write(body); err != nil {
		c.dropPending(id)
		return nil, err
	}
	select {
	case r := <-ch:
		return r.result, r.err
	case <-ctx.Done():
		c.dropPending(id)
		return nil, ctx.Err()
	case <-c.done:
		c.dropPending(id)
		if c.err != nil {
			return nil, c.err
		}
		return nil, errors.New("lsp: server exited")
	}
}

// Notify sends a notification.
func (c *Client) Notify(method string, params any) error {
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return err
	}
	return c.write(body)
}

func (c *Client) dropPending(id int64) {
	c.pendMu.Lock()
	delete(c.pending, id)
	c.pendMu.Unlock()
}

func (c *Client) write(body []byte) error {
	select {
	case <-c.done:
		return errors.New("lsp: client closed")
	default:
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := io.WriteString(c.stdin, header); err != nil {
		return err
	}
	_, err := c.stdin.Write(body)
	return err
}

func (c *Client) readLoop() {
	r := bufio.NewReaderSize(c.stdout, 64*1024)
	for {
		body, err := readFrame(r)
		if err != nil {
			c.fail(err)
			return
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			c.logs.Add("invalid json from server: " + err.Error())
			continue
		}
		switch {
		case env.Method == "" && len(env.ID) > 0:
			var id int64
			if err := json.Unmarshal(env.ID, &id); err != nil {
				continue
			}
			c.pendMu.Lock()
			ch := c.pending[id]
			delete(c.pending, id)
			c.pendMu.Unlock()
			if ch == nil {
				continue
			}
			if env.Error != nil {
				ch <- reply{err: env.Error}
			} else {
				ch <- reply{result: env.Result}
			}
		case env.Method != "" && len(env.ID) > 0:
			go c.handleServerRequest(env)
		case env.Method != "":
			if c.notify != nil {
				c.notify(env.Method, env.Params)
			}
		}
	}
}

func (c *Client) handleServerRequest(env envelope) {
	result, rpcErr := serverRequestResult(env.Method, env.Params)
	resp := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(env.ID)}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		resp["result"] = result
	}
	body, err := json.Marshal(resp)
	if err == nil {
		_ = c.write(body)
	}
}

// serverRequestResult is the pure dispatch used for requests initiated by a
// language server. Keeping it separate makes the read-only guarantees
// testable without a live server.
func serverRequestResult(method string, params json.RawMessage) (any, *ResponseError) {
	switch method {
	case "workspace/applyEdit":
		return map[string]any{
			"applied":       false,
			"failureReason": "VibeDE is a read-only IDE; workspace edits are disabled",
		}, nil
	case "workspace/configuration":
		var p struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(params, &p)
		vals := make([]any, len(p.Items))
		for i := range vals {
			vals[i] = defaultClientConfig(p.Items[i].Section)
		}
		return vals, nil
	case "workspace/workspaceFolders":
		return workspaceFolders, nil
	case "client/registerCapability", "client/unregisterCapability",
		"window/workDoneProgress/create", "window/showMessageRequest",
		"window/showDocument", "workspace/codeLens/refresh",
		"workspace/diagnostic/refresh", "workspace/inlayHint/refresh",
		"workspace/semanticTokens/refresh":
		return nil, nil
	default:
		return nil, &ResponseError{Code: -32601, Message: "method not supported by VibeDE: " + method}
	}
}

var workspaceFolders any // set by Manager before starting clients

// defaultClientConfig supplies the settings that VSCode-family language
// servers need. Returning null here crashes vscode-css-language-server and
// leaves HTML validation disabled.
func defaultClientConfig(section string) map[string]any {
	switch section {
	case "css", "scss", "less", "html", "javascript", "typescript", "js/ts":
		return map[string]any{"validate": true}
	case "json", "jsonc":
		return map[string]any{"validate": map[string]any{"enable": true}}
	default:
		return map[string]any{}
	}
}

// clientSettings is sent in workspace/didChangeConfiguration.
var clientSettings = map[string]any{
	"css":        defaultClientConfig("css"),
	"scss":       defaultClientConfig("scss"),
	"less":       defaultClientConfig("less"),
	"html":       defaultClientConfig("html"),
	"javascript": defaultClientConfig("javascript"),
	"typescript": defaultClientConfig("typescript"),
	"js/ts":      defaultClientConfig("js/ts"),
	"json":       defaultClientConfig("json"),
	"jsonc":      defaultClientConfig("jsonc"),
}

func readFrame(r *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(strings.ToLower(line), "content-length:") {
			v := strings.TrimSpace(line[len("content-length:"):])
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 || n > 64<<20 {
				return nil, fmt.Errorf("lsp: invalid Content-Length %q", v)
			}
			length = n
		}
	}
	if length < 0 {
		return nil, errors.New("lsp: missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

func (c *Client) fail(err error) {
	c.once.Do(func() {
		c.err = err
		close(c.done)
		c.pendMu.Lock()
		for id, ch := range c.pending {
			ch <- reply{err: fmt.Errorf("lsp: transport closed: %w", err)}
			delete(c.pending, id)
		}
		c.pendMu.Unlock()
		if c.cmd.Process != nil {
			_ = c.cmd.Process.Kill()
		}
		_ = c.cmd.Wait()
	})
}

// Close performs the LSP shutdown handshake and terminates the process.
func (c *Client) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	_, _ = c.Call(ctx, mShutdown, nil)
	cancel()
	_ = c.Notify(mExit, nil)
	_ = c.stdin.Close()
	select {
	case <-c.done:
	case <-time.After(2 * time.Second):
		c.fail(errors.New("shutdown timeout"))
	}
}
