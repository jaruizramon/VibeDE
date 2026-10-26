// Package server exposes VibeDE's read-only HTTP API and embedded UI.
//
// There is deliberately no write endpoint. The only POST routes are queries
// (open document, navigate, hover, restart server) plus a diagnostic rescan.
package server

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vibede/internal/analyze"
	"vibede/internal/config"
	"vibede/internal/lsp"
	"vibede/internal/version"
	"vibede/internal/watch"
	"vibede/internal/workspace"
)

//go:embed web/*
var webFS embed.FS

// Server owns the workspace view, watcher, language servers and analysis.
type Server struct {
	cfg     config.Config
	ws      *workspace.Workspace
	tracker *watch.Tracker
	watcher *watch.Watcher
	lsp     *lsp.Manager
	engine  *analyze.Engine
	broker  *broker
	static  map[string][]byte

	start   time.Time
	ctx     context.Context
	cancel  context.CancelFunc
	closeMu sync.Once

	windowMu     sync.Mutex
	windowOpener func(url, title string) error
}

// New builds the server and starts the filesystem watcher. Call Close when
// done.
func New(cfg config.Config) (*Server, error) {
	ws := workspace.New(cfg)
	assets, err := loadWebAssets()
	if err != nil {
		return nil, err
	}
	if _, ok := assets["app.wasm"]; !ok {
		return nil, fmt.Errorf("browser client is not built; run `make web`")
	}
	if _, ok := assets["wasm_exec.js"]; !ok {
		return nil, fmt.Errorf("wasm_exec.js is missing; run `make web`")
	}
	ctx, cancel := context.WithCancel(context.Background())
	tracker := watch.NewTracker(ws, cfg.Limits.MaxSnapshotBytes, cfg.Limits.SnapshotBudget, cfg.Limits.DiffComplexityCap)
	b := newBroker(256, 80)
	s := &Server{
		cfg:     cfg,
		ws:      ws,
		tracker: tracker,
		engine:  analyze.NewEngine(ws, cfg),
		broker:  b,
		static:  assets,
		start:   time.Now().UTC(),
		ctx:     ctx,
		cancel:  cancel,
	}
	s.lsp = lsp.NewManager(ws, cfg, func(ev lsp.Event) {
		switch ev.Type {
		case "server":
			if ev.Server != nil {
				b.Publish("server", ev.Server)
			}
		case "diagnostics":
			if ev.Diagnostics != nil {
				b.Publish("diagnostics", ev.Diagnostics)
			}
		}
	})
	watcher, err := watch.New(ws, tracker, cfg.Debounce(), func(ch watch.Change) {
		s.engine.Invalidate(ch.Path)
		if !ch.IsDir {
			go s.lsp.Change(ch.Path)
		}
		b.Publish("change", ch)
	})
	if err != nil {
		cancel()
		s.lsp.Close()
		return nil, fmt.Errorf("watcher: %w", err)
	}
	s.watcher = watcher
	go watcher.Run(ctx)
	go tracker.Seed(ctx)
	return s, nil
}

// Close stops watchers, language servers and open SSE clients.
func (s *Server) Close() {
	s.closeMu.Do(func() {
		s.cancel()
		if s.watcher != nil {
			_ = s.watcher.Close()
		}
		s.lsp.Close()
		s.broker.Close()
	})
}

// Handler returns the complete HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/info", s.handleInfo)
	mux.HandleFunc("GET /api/tree", s.handleTree)
	mux.HandleFunc("GET /api/file", s.handleFile)
	mux.HandleFunc("GET /api/analysis", s.handleAnalysis)
	mux.HandleFunc("GET /api/diagnostics", s.handleDiagnostics)
	mux.HandleFunc("GET /api/servers", s.handleServers)
	mux.HandleFunc("GET /api/symbols", s.handleSymbols)
	mux.HandleFunc("GET /api/diagram", s.handleDiagram)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/search/code", s.handleCodeSearch)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("POST /api/lsp/open", s.handleLSPOpen)
	mux.HandleFunc("POST /api/lsp/close", s.handleLSPClose)
	mux.HandleFunc("POST /api/navigate", s.handleNavigate)
	mux.HandleFunc("POST /api/hover", s.handleHover)
	mux.HandleFunc("POST /api/servers/restart", s.handleServerRestart)
	mux.HandleFunc("POST /api/servers/rescan", s.handleServerRescan)
	mux.HandleFunc("POST /api/detach", s.handleDetach)
	mux.HandleFunc("GET /", s.handleStatic)
	return s.middleware(mux)
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline' 'wasm-unsafe-eval'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; font-src 'self' data:; base-uri 'none'; form-action 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

func loadWebAssets() (map[string][]byte, error) {
	out := make(map[string][]byte)
	entries, err := fs.ReadDir(webFS, "web")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		b, err := webFS.ReadFile("web/" + e.Name())
		if err != nil {
			return nil, err
		}
		out[e.Name()] = b
	}
	return out, nil
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = "index.html"
	}
	b, ok := s.static[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	switch name {
	case "app.wasm":
		w.Header().Set("Content-Type", "application/wasm")
	case "wasm_exec.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case "style.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case "index.html":
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	default:
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// ---- JSON helpers ----

type apiError struct {
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
	Install string `json:"install,omitempty"`
	Docs    string `json:"docs,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, apiError{Error: msg})
}

func (s *Server) writeFail(w http.ResponseWriter, err error) {
	var unavailable *lsp.ErrUnavailable
	if errors.As(err, &unavailable) {
		writeJSON(w, http.StatusServiceUnavailable, apiError{
			Error:   unavailable.Error(),
			Code:    "server_unavailable",
			Install: unavailable.Install,
		})
		return
	}
	if errors.Is(err, workspace.ErrOutsideRoot) {
		writeJSON(w, http.StatusForbidden, apiError{Error: "path is outside the workspace", Code: "outside_root"})
		return
	}
	if errors.Is(err, workspace.ErrBinary) {
		writeJSON(w, http.StatusUnsupportedMediaType, apiError{Error: "binary file is not previewable", Code: "binary"})
		return
	}
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(w, http.StatusNotFound, apiError{Error: "file not found", Code: "not_found"})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) {
		writeJSON(w, http.StatusGatewayTimeout, apiError{Error: "language server timed out", Code: "timeout"})
		return
	}
	if errors.Is(err, context.Canceled) {
		writeJSON(w, 499, apiError{Error: "request canceled", Code: "canceled"})
		return
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "exceeds the"):
		writeJSON(w, http.StatusRequestEntityTooLarge, apiError{Error: msg, Code: "too_large"})
	case strings.Contains(msg, "not found"):
		writeJSON(w, http.StatusNotFound, apiError{Error: msg, Code: "not_found"})
	case strings.Contains(msg, "not a directory"), strings.Contains(msg, "is a directory"),
		strings.Contains(msg, "not a regular file"):
		writeJSON(w, http.StatusBadRequest, apiError{Error: msg, Code: "bad_path"})
	default:
		writeJSON(w, http.StatusInternalServerError, apiError{Error: msg, Code: "internal"})
	}
}

func getJSONBody(r *http.Request, dst any) error {
	defer r.Body.Close()
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, dst)
}

func (s *Server) relParam(r *http.Request) (string, bool) {
	rel := r.URL.Query().Get("path")
	if strings.ContainsRune(rel, 0) {
		return "", false
	}
	return rel, true
}

// Info is the UI bootstrap payload.
type Info struct {
	Name       string           `json:"name"`
	Version    string           `json:"version"`
	Root       string           `json:"root"`
	ReadOnly   bool             `json:"readOnly"`
	Detachable bool             `json:"detachable"`
	StartedAt  time.Time        `json:"startedAt"`
	Languages  []string         `json:"languages"`
	Limits     config.Limits    `json:"limits"`
	Servers    []lsp.ServerInfo `json:"servers"`
	Watcher    WatcherInfo      `json:"watcher"`
	Features   []string         `json:"features"`
	History    int              `json:"history"`
}

type WatcherInfo struct {
	Seeded    bool  `json:"seeded"`
	Snapshots int   `json:"snapshots"`
	Bytes     int64 `json:"snapshotBytes"`
}

func (s *Server) info() Info {
	files, bytes := s.tracker.Stats()
	return Info{
		Name:       "VibeDE",
		Version:    version.Version,
		Root:       s.ws.Root(),
		ReadOnly:   true,
		Detachable: s.hasWindowOpener(),
		StartedAt:  s.start,
		Languages:  []string{"go", "python", "javascript", "typescript", "html", "css", "c", "cpp", "rust", "java"},
		Limits:     s.cfg.Limits,
		Servers:    s.lsp.Info(),
		Watcher:    WatcherInfo{Seeded: s.tracker.Seeded(), Snapshots: files, Bytes: bytes},
		Features: []string{
			"Live agent edit stream with line diffs",
			"Language-server diagnostics and navigation",
			"Structure, cyclomatic complexity and Big-O per function",
			"Deterministic flowcharts, UML, use-case, sequence and import diagrams",
		},
		History: len(s.changesHistory()),
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version.Version})
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.info())
}

type treeResponse struct {
	Path    string            `json:"path"`
	Entries []workspace.Entry `json:"entries"`
}

func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.relParam(r)
	entries, err := s.ws.List(rel)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	if entries == nil {
		entries = []workspace.Entry{}
	}
	writeJSON(w, http.StatusOK, treeResponse{Path: rel, Entries: entries})
}

type fileResponse struct {
	Path     string    `json:"path"`
	Language string    `json:"language"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	Lines    int       `json:"lines"`
	Content  string    `json:"content"`
}

func (s *Server) handleFile(w http.ResponseWriter, r *http.Request) {
	rel, ok := s.relParam(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	content, err := s.ws.Read(rel)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	st, err := s.ws.Stat(rel)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	s.tracker.Observe(rel, content)
	writeJSON(w, http.StatusOK, fileResponse{
		Path:     rel,
		Language: workspace.Language(rel),
		Size:     st.Size,
		Modified: st.Modified,
		Lines:    strings.Count(string(content), "\n") + 1,
		Content:  string(content),
	})
}

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.relParam(r)
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	a, err := s.engine.Analyze(rel)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.relParam(r)
	if rel != "" {
		writeJSON(w, http.StatusOK, s.lsp.DiagnosticsFor(rel))
		return
	}
	files := s.lsp.AllDiagnostics()
	if files == nil {
		files = []lsp.FileDiagnostics{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files})
}

func (s *Server) handleServers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.lsp.Info()})
}

func (s *Server) handleSymbols(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.relParam(r)
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	syms, err := s.lsp.Symbols(ctx, rel)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	if syms == nil {
		syms = []lsp.Symbol{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"symbols": syms})
}

func (s *Server) handleDiagram(w http.ResponseWriter, r *http.Request) {
	rel, _ := s.relParam(r)
	if rel == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	kind := r.URL.Query().Get("kind")
	function := r.URL.Query().Get("function")
	doc, err := s.engine.Diagram(rel, kind, function)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

type searchResult struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Language string `json:"language"`
	IsDir    bool   `json:"isDir,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 1 || len(q) > 100 {
		writeJSON(w, http.StatusOK, map[string]any{"results": []searchResult{}})
		return
	}
	lower := strings.ToLower(q)
	var results []searchResult
	visited := 0
	_ = s.ws.WalkFiles(func(rel string, size int64) bool {
		visited++
		if visited > 30000 || len(results) >= 60 {
			return false
		}
		base := strings.ToLower(path.Base(rel))
		lr := strings.ToLower(rel)
		if strings.Contains(base, lower) || strings.Contains(lr, lower) {
			results = append(results, searchResult{
				Path:     rel,
				Name:     path.Base(rel),
				Language: workspace.Language(rel),
			})
		}
		return true
	})
	sort.SliceStable(results, func(i, j int) bool {
		bi := strings.HasPrefix(strings.ToLower(results[i].Name), lower)
		bj := strings.HasPrefix(strings.ToLower(results[j].Name), lower)
		if bi != bj {
			return bi
		}
		return results[i].Path < results[j].Path
	})
	if results == nil {
		results = []searchResult{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleLSPOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := getJSONBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	key, err := s.lsp.Open(ctx, req.Path)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"server": key, "diagnostics": s.lsp.DiagnosticsFor(req.Path)})
}

func (s *Server) handleLSPClose(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if err := getJSONBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	s.lsp.CloseDoc(req.Path)
	writeJSON(w, http.StatusOK, map[string]any{"closed": req.Path})
}

type navRequest struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Character int    `json:"character"`
	Method    string `json:"method"`
}

func (s *Server) handleNavigate(w http.ResponseWriter, r *http.Request) {
	var req navRequest
	if err := getJSONBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Path == "" || req.Line < 0 || req.Character < 0 || req.Line > 10_000_000 || req.Character > 1_000_000 {
		writeErr(w, http.StatusBadRequest, "path and non-negative position are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	locations, err := s.lsp.Navigate(ctx, req.Path, req.Line, req.Character, req.Method)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	if locations == nil {
		locations = []lsp.NavLocation{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"locations": locations, "method": req.Method})
}

func (s *Server) handleHover(w http.ResponseWriter, r *http.Request) {
	var req navRequest
	if err := getJSONBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Path == "" || req.Line < 0 || req.Character < 0 {
		writeErr(w, http.StatusBadRequest, "path and non-negative position are required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hover, err := s.lsp.Hover(ctx, req.Path, req.Line, req.Character)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, hover)
}

func (s *Server) handleServerRestart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key string `json:"key"`
	}
	if err := getJSONBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Key == "" {
		writeErr(w, http.StatusBadRequest, "key is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	if err := s.lsp.Restart(ctx, req.Key); err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.lsp.Info()})
}

func (s *Server) handleServerRescan(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"servers": s.lsp.Refresh()})
}

func (s *Server) changesHistory() []watch.Change {
	var out []watch.Change
	for _, ev := range s.broker.Recent() {
		if ev.Name != "change" {
			continue
		}
		var ch watch.Change
		if json.Unmarshal(ev.Data, &ch) == nil {
			out = append(out, ch)
		}
	}
	return out
}

// SetWindowOpener enables the native "detach into a new window" action. In
// browser/headless mode no opener is installed and the endpoint returns 501.
func (s *Server) SetWindowOpener(fn func(url, title string) error) {
	s.windowMu.Lock()
	s.windowOpener = fn
	s.windowMu.Unlock()
}

func (s *Server) hasWindowOpener() bool {
	s.windowMu.Lock()
	defer s.windowMu.Unlock()
	return s.windowOpener != nil
}

func (s *Server) handleDetach(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	}
	if err := getJSONBody(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Path == "" {
		writeErr(w, http.StatusBadRequest, "path is required")
		return
	}
	entry, err := s.ws.Stat(req.Path)
	if err != nil {
		s.writeFail(w, err)
		return
	}
	if entry.IsDir {
		writeErr(w, http.StatusBadRequest, "only files can be detached")
		return
	}
	s.windowMu.Lock()
	opener := s.windowOpener
	s.windowMu.Unlock()
	if opener == nil {
		writeErr(w, http.StatusNotImplemented, "detach is only available in native mode")
		return
	}
	q := url.Values{}
	q.Set("detached", "1")
	q.Set("path", req.Path)
	if req.Line > 0 {
		q.Set("line", strconv.Itoa(req.Line))
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	target := scheme + "://" + r.Host + "/?" + q.Encode()
	title := "VibeDE — " + path.Base(req.Path)
	if err := opener(target, title); err != nil {
		s.writeFail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"detached": true, "title": title})
}
