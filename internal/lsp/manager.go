package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vibede/internal/config"
	"vibede/internal/version"
	"vibede/internal/workspace"
)

// ErrUnavailable says that no language server is installed for a file.
type ErrUnavailable struct {
	Key     string
	Reason  string
	Install string
}

func (e *ErrUnavailable) Error() string {
	if e.Reason == "" {
		return "no language server configured for " + e.Key
	}
	return e.Reason
}

// Event is emitted by the manager when a server or diagnostics change.
// Handlers must not block.
type Event struct {
	Type        string           `json:"type"` // server | diagnostics
	Server      *ServerInfo      `json:"server,omitempty"`
	Diagnostics *FileDiagnostics `json:"diagnostics,omitempty"`
}

// ServerInfo is the UI/API view of one language server.
type ServerInfo struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Languages   []string `json:"languages"`
	Installed   bool     `json:"installed"`
	Status      string   `json:"status"` // available, starting, running, failed, stopped, missing, disabled
	Command     string   `json:"command"`
	Args        []string `json:"args,omitempty"`
	Install     string   `json:"install,omitempty"`
	Docs        string   `json:"docs,omitempty"`
	Error       string   `json:"error,omitempty"`
	PID         int      `json:"pid,omitempty"`
	OpenDocs    int      `json:"openDocs"`
	Diagnostics int      `json:"diagnostics"`
	Logs        []string `json:"logs,omitempty"`
}

// Manager owns one session per language-server group, started lazily.
type Manager struct {
	ws      *workspace.Workspace
	cfg     config.Config
	onEvent func(Event)

	mu        sync.Mutex
	dirs      []string
	nodePaths []string
	specs     map[string]*resolvedSpec
	sessions  map[string]*Session
	closed    bool
}

// NewManager resolves language-server binaries but starts nothing.
func NewManager(ws *workspace.Workspace, cfg config.Config, onEvent func(Event)) *Manager {
	dirs := searchDirs()
	nodePaths := nodeModuleDirs()
	workspaceFolders = []map[string]any{{
		"uri":  PathToURI(ws.Root()),
		"name": filepath.Base(ws.Root()),
	}}
	overrides := cfg.Servers
	if cfg.NoServers {
		overrides = make(map[string]config.ServerOverride, len(registry))
		for _, s := range registry {
			overrides[s.Key] = config.ServerOverride{Disabled: true}
		}
	}
	specs := resolve(dirs, overrides)
	augmentServers(specs, nodePaths, ws.Root())
	return &Manager{
		ws:        ws,
		cfg:       cfg,
		onEvent:   onEvent,
		dirs:      dirs,
		nodePaths: nodePaths,
		specs:     specs,
		sessions:  make(map[string]*Session),
	}
}

// Info returns every registry entry, sorted by registry order.
func (m *Manager) Info() []ServerInfo {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	sessions := make(map[string]*Session, len(m.sessions))
	for k, v := range m.sessions {
		sessions[k] = v
	}
	m.mu.Unlock()
	out := make([]ServerInfo, 0, len(registry))
	for _, key := range specKeys() {
		out = append(out, m.infoForKey(key, sessions[key]))
	}
	return out
}

func (m *Manager) infoForKey(key string, s *Session) ServerInfo {
	m.mu.Lock()
	rs := m.specs[key]
	m.mu.Unlock()
	if rs == nil {
		return ServerInfo{Key: key, Status: "missing"}
	}
	info := ServerInfo{
		Key:       key,
		Name:      rs.Spec.Name,
		Languages: append([]string(nil), rs.Spec.Languages...),
		Installed: rs.Installed,
		Command:   rs.Command,
		Args:      append([]string(nil), rs.Args...),
		Install:   rs.Spec.Install,
		Docs:      rs.Spec.Docs,
	}
	if rs.Disabled {
		info.Status = "disabled"
		return info
	}
	if !rs.Installed {
		info.Status = "missing"
		info.Error = rs.Err
		return info
	}
	if s == nil {
		info.Status = "available"
		return info
	}
	info.Status = s.status
	info.Error = s.errMsg
	info.PID = s.pid
	info.OpenDocs = s.docCount()
	info.Diagnostics = s.diagCount()
	info.Logs = s.logs(40)
	if s.status == "" {
		info.Status = "available"
	}
	return info
}

// Refresh re-discovers binaries and emits server status events.
func (m *Manager) Refresh() []ServerInfo {
	dirs := searchDirs()
	nodePaths := nodeModuleDirs()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.dirs = dirs
	m.nodePaths = nodePaths
	overrides := m.cfg.Servers
	if m.cfg.NoServers {
		overrides = make(map[string]config.ServerOverride, len(registry))
		for _, s := range registry {
			overrides[s.Key] = config.ServerOverride{Disabled: true}
		}
	}
	specs := resolve(dirs, overrides)
	augmentServers(specs, nodePaths, m.ws.Root())
	m.specs = specs
	m.mu.Unlock()
	infos := m.Info()
	for i := range infos {
		m.emit(Event{Type: "server", Server: &infos[i]})
	}
	return infos
}

func (m *Manager) spec(key string) *resolvedSpec {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.specs[key]
}

func (m *Manager) session(key string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[key]; s != nil {
		return s
	}
	rs := m.specs[key]
	if rs == nil {
		return nil
	}
	s := &Session{
		m:     m,
		key:   key,
		spec:  rs,
		docs:  make(map[string]*openDoc),
		diags: make(map[string][]Diagnostic),
	}
	m.sessions[key] = s
	return s
}

func (m *Manager) emit(ev Event) {
	m.mu.Lock()
	closed := m.closed
	fn := m.onEvent
	m.mu.Unlock()
	if !closed && fn != nil {
		fn(ev)
	}
}

func (m *Manager) available(key string) error {
	rs := m.spec(key)
	if rs == nil {
		return &ErrUnavailable{Key: key, Reason: "no language server for this file type"}
	}
	if rs.Disabled {
		return &ErrUnavailable{Key: key, Reason: rs.Spec.Name + " is disabled by configuration", Install: rs.Spec.Install}
	}
	if !rs.Installed {
		reason := rs.Err
		if reason == "" {
			reason = rs.Spec.Name + " is not installed"
		}
		return &ErrUnavailable{Key: key, Reason: reason, Install: rs.Spec.Install}
	}
	return nil
}

// Open ensures a document is synchronized with its language server. It starts
// the server on first use. The returned key is the server group.
func (m *Manager) Open(ctx context.Context, rel string) (string, error) {
	if _, err := m.ws.Resolve(rel); err != nil {
		return "", err
	}
	lang := workspace.Language(rel)
	key := workspace.ServerGroup(lang)
	if key == "" {
		return "", &ErrUnavailable{Key: lang, Reason: "no language server for " + lang}
	}
	if err := m.available(key); err != nil {
		return "", err
	}
	s := m.session(key)
	if s == nil {
		return "", &ErrUnavailable{Key: key, Reason: "no language server configured"}
	}
	if _, err := s.ensure(ctx); err != nil {
		return key, err
	}
	if err := s.open(ctx, rel, lang); err != nil {
		return key, err
	}
	return key, nil
}

// Change mirrors a filesystem change into an open document.
func (m *Manager) Change(rel string) {
	lang := workspace.Language(rel)
	key := workspace.ServerGroup(lang)
	if key == "" {
		return
	}
	m.mu.Lock()
	s := m.sessions[key]
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.change(rel)
}

// CloseDoc stops synchronizing a document and clears its diagnostics.
func (m *Manager) CloseDoc(rel string) {
	lang := workspace.Language(rel)
	key := workspace.ServerGroup(lang)
	m.mu.Lock()
	s := m.sessions[key]
	m.mu.Unlock()
	if s != nil {
		s.closeDoc(rel)
	}
}

// Navigate runs definition, references, implementation or typeDefinition.
func (m *Manager) Navigate(ctx context.Context, rel string, line, char int, method string) ([]NavLocation, error) {
	var lspMethod string
	switch method {
	case "", "definition":
		lspMethod = mDefinition
	case "references":
		lspMethod = mReferences
	case "implementation":
		lspMethod = mImplementation
	case "typeDefinition":
		lspMethod = mTypeDefinition
	default:
		return nil, fmt.Errorf("unsupported navigation method %q", method)
	}
	key, err := m.Open(ctx, rel)
	if err != nil {
		return nil, err
	}
	s := m.session(key)
	c := s.current()
	if c == nil {
		return nil, &ErrUnavailable{Key: key, Reason: "language server is not running"}
	}
	uri := PathToURI(s.abs(rel))
	pos := TextDocumentPosition{Position: Position{Line: line, Character: char}}
	pos.TextDocument.URI = uri
	var params any = pos
	if lspMethod == mReferences {
		rp := referencesParams{TextDocumentPosition: pos}
		rp.Context.IncludeDeclaration = true
		params = rp
	}
	raw, err := c.Call(ctx, lspMethod, params)
	if err != nil {
		return nil, err
	}
	return parseLocations(raw, m.pathForURI), nil
}

// Hover returns markdown/plaintext hover content at a position.
func (m *Manager) Hover(ctx context.Context, rel string, line, char int) (Hover, error) {
	key, err := m.Open(ctx, rel)
	if err != nil {
		return Hover{}, err
	}
	s := m.session(key)
	c := s.current()
	if c == nil {
		return Hover{}, &ErrUnavailable{Key: key, Reason: "language server is not running"}
	}
	uri := PathToURI(s.abs(rel))
	pos := TextDocumentPosition{Position: Position{Line: line, Character: char}}
	pos.TextDocument.URI = uri
	raw, err := c.Call(ctx, mHover, pos)
	if err != nil {
		return Hover{}, err
	}
	return parseHover(raw), nil
}

// Symbols returns the language server's document outline. It is used to
// complement the native structure analysis.
func (m *Manager) Symbols(ctx context.Context, rel string) ([]Symbol, error) {
	key, err := m.Open(ctx, rel)
	if err != nil {
		return nil, err
	}
	s := m.session(key)
	c := s.current()
	if c == nil {
		return nil, &ErrUnavailable{Key: key, Reason: "language server is not running"}
	}
	uri := PathToURI(s.abs(rel))
	p := documentSymbolsParams{}
	p.TextDocument.URI = uri
	raw, err := c.Call(ctx, mDocumentSymbol, p)
	if err != nil {
		return nil, err
	}
	return parseSymbols(raw, m.pathForURI), nil
}

// AllDiagnostics returns diagnostics for every file, sorted deterministically.
func (m *Manager) AllDiagnostics() []FileDiagnostics {
	m.mu.Lock()
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	out := make([]FileDiagnostics, 0)
	for _, s := range sessions {
		out = append(out, s.diagnostics()...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// DiagnosticsFor returns diagnostics for one file.
func (m *Manager) DiagnosticsFor(rel string) FileDiagnostics {
	lang := workspace.Language(rel)
	key := workspace.ServerGroup(lang)
	m.mu.Lock()
	s := m.sessions[key]
	m.mu.Unlock()
	if s == nil {
		return FileDiagnostics{Path: rel}
	}
	return s.diagnosticsFor(rel)
}

// Restart stops and restarts one server, reopening its documents.
func (m *Manager) Restart(ctx context.Context, key string) error {
	if err := m.available(key); err != nil {
		return err
	}
	s := m.session(key)
	if s == nil {
		return &ErrUnavailable{Key: key, Reason: "no language server configured"}
	}
	if rs := m.spec(key); rs != nil {
		s.mu.Lock()
		s.spec = rs
		s.mu.Unlock()
	}
	return s.restart(ctx)
}

// Close stops every running server.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	sessions := make([]*Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		sessions = append(sessions, s)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.stop()
	}
}

func (m *Manager) pathForURI(uri string) (string, bool) {
	abs, err := PathFromURI(uri)
	if err != nil {
		return "", false
	}
	if !filepath.IsAbs(abs) {
		return "", false
	}
	return m.ws.Rel(filepath.Clean(abs))
}

func augmentServers(specs map[string]*resolvedSpec, nodePaths []string, root string) {
	ts, ok := specs["typescript"]
	if !ok || ts == nil || !ts.Installed || ts.Source == "config" {
		return
	}
	for _, arg := range ts.Args {
		if arg == "--tsserver-path" {
			return
		}
	}
	if p := tsserverPath(nodePaths, root); p != "" {
		ts.InitOptions = map[string]any{"tsserver": map[string]any{"path": p}}
	}
}

// ---- Session ----

type openDoc struct {
	rel      string
	uri      string
	language string
	version  int
	text     string
}

type Session struct {
	m    *Manager
	key  string
	spec *resolvedSpec

	ensureMu sync.Mutex
	mu       sync.Mutex
	client   *Client
	status   string
	errMsg   string
	pid      int
	gen      int
	closing  bool

	docMu sync.Mutex
	docs  map[string]*openDoc

	diagMu sync.Mutex
	diags  map[string][]Diagnostic
}

func (s *Session) current() *Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.client
}

func (s *Session) abs(rel string) string {
	abs, err := s.m.ws.Resolve(rel)
	if err != nil {
		return filepath.Join(s.m.ws.Root(), filepath.FromSlash(rel))
	}
	return abs
}

func (s *Session) logs(n int) []string {
	c := s.current()
	if c == nil {
		return nil
	}
	return c.Logs(n)
}

func (s *Session) docCount() int {
	s.docMu.Lock()
	defer s.docMu.Unlock()
	return len(s.docs)
}

func (s *Session) diagCount() int {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	n := 0
	for _, ds := range s.diags {
		n += len(ds)
	}
	return n
}

func (s *Session) setStatus(status, errMsg string) {
	s.mu.Lock()
	s.status = status
	s.errMsg = errMsg
	s.mu.Unlock()
}

func (s *Session) emitInfo() {
	info := s.m.infoForKey(s.key, s)
	s.m.emit(Event{Type: "server", Server: &info})
}

func (s *Session) ensure(ctx context.Context) (*Client, error) {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	s.mu.Lock()
	if s.client != nil {
		c := s.client
		s.mu.Unlock()
		return c, nil
	}
	s.mu.Unlock()
	return s.startLocked(ctx)
}

// startLocked starts the process and initializes it. Caller holds ensureMu.
func (s *Session) startLocked(ctx context.Context) (*Client, error) {
	if s.spec == nil || !s.spec.Installed || s.spec.Disabled {
		reason := "language server unavailable"
		if s.spec != nil && s.spec.Err != "" {
			reason = s.spec.Err
		}
		return nil, &ErrUnavailable{Key: s.key, Reason: reason}
	}
	s.mu.Lock()
	s.closing = false
	s.status = "starting"
	s.errMsg = ""
	s.gen++
	gen := s.gen
	s.mu.Unlock()
	s.emitInfo()

	m := s.m
	m.mu.Lock()
	dirs := append([]string(nil), m.dirs...)
	nodePaths := append([]string(nil), m.nodePaths...)
	m.mu.Unlock()
	notify := func(method string, params json.RawMessage) { s.handleNotify(method, params) }
	c, err := StartClient(s.spec.Command, s.spec.Args, envFor(dirs, nodePaths), m.ws.Root(), notify)
	if err != nil {
		s.setStatus("failed", "start: "+err.Error())
		s.emitInfo()
		return nil, err
	}
	initCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	raw, err := c.Call(initCtx, mInitialize, s.initParams())
	cancel()
	if err != nil {
		c.Close()
		s.setStatus("failed", "initialize: "+err.Error())
		s.emitInfo()
		return nil, err
	}
	var initRes InitializeResult
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &initRes)
	}
	_ = c.Notify(mInitialized, map[string]any{})
	_ = c.Notify(mDidChangeConfig, map[string]any{"settings": clientSettings})

	s.mu.Lock()
	s.client = c
	s.status = "running"
	s.pid = 0
	if c.cmd.Process != nil {
		s.pid = c.cmd.Process.Pid
	}
	s.mu.Unlock()
	s.emitInfo()
	go s.watchClient(c, gen)
	return c, nil
}

func (s *Session) initParams() map[string]any {
	root := s.m.ws.Root()
	params := map[string]any{
		"processId": os.Getpid(),
		"clientInfo": map[string]any{
			"name":    "VibeDE",
			"version": version.Version,
		},
		"rootPath": root,
		"rootUri":  PathToURI(root),
		"workspaceFolders": []map[string]any{{
			"uri":  PathToURI(root),
			"name": filepath.Base(root),
		}},
		"trace":                 "off",
		"initializationOptions": s.spec.InitOptions,
		"capabilities": map[string]any{
			"workspace": map[string]any{
				"workspaceFolders": true,
				"configuration":    true,
				"applyEdit":        false,
				"workspaceEdit": map[string]any{
					"documentChanges": false,
				},
			},
			"textDocument": map[string]any{
				"synchronization": map[string]any{
					"dynamicRegistration": false,
					"willSave":            false,
					"willSaveWaitUntil":   false,
					"didSave":             false,
				},
				"definition":     map[string]any{"linkSupport": true},
				"typeDefinition": map[string]any{"linkSupport": true},
				"implementation": map[string]any{"linkSupport": true},
				"references":     map[string]any{},
				"hover": map[string]any{
					"contentFormat": []string{"markdown", "plaintext"},
				},
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
					"symbolKind": map[string]any{
						"valueSet": symbolKindValues(),
					},
				},
				"publishDiagnostics": map[string]any{
					"relatedInformation":     true,
					"versionSupport":         true,
					"codeDescriptionSupport": true,
					"dataSupport":            false,
				},
			},
			"window": map[string]any{
				"workDoneProgress": false,
			},
			"general": map[string]any{
				"positionEncodings": []string{"utf-16"},
			},
		},
	}
	if s.spec.InitOptions != nil {
		params["initializationOptions"] = s.spec.InitOptions
	}
	return params
}

func symbolKindValues() []int {
	out := make([]int, 0, len(SymbolKind))
	for k := range SymbolKind {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

func (s *Session) watchClient(c *Client, gen int) {
	<-c.Done()
	s.mu.Lock()
	if s.client != c || s.gen != gen || s.closing {
		s.mu.Unlock()
		return
	}
	s.client = nil
	s.status = "failed"
	s.pid = 0
	s.errMsg = "language server exited"
	if err := c.Err(); err != nil && !errors.Is(err, os.ErrClosed) {
		s.errMsg = "language server exited: " + err.Error()
	}
	s.mu.Unlock()
	s.emitInfo()
}

func (s *Session) open(ctx context.Context, rel, lang string) error {
	abs, err := s.m.ws.Resolve(rel)
	if err != nil {
		return err
	}
	text, err := s.m.ws.Read(rel)
	if err != nil {
		return err
	}
	uri := PathToURI(abs)
	s.docMu.Lock()
	defer s.docMu.Unlock()
	if doc := s.docs[rel]; doc != nil {
		if doc.text == string(text) {
			return nil
		}
		c := s.current()
		if c == nil {
			return &ErrUnavailable{Key: s.key, Reason: "language server is not running"}
		}
		doc.text = string(text)
		doc.version++
		version := doc.version
		doc.uri = uri
		return c.Notify(mDidChange, didChangeParams{
			TextDocument: struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
			}{URI: uri, Version: version},
			ContentChanges: []struct {
				Text string `json:"text"`
			}{{Text: string(text)}},
		})
	}
	c := s.current()
	if c == nil {
		return &ErrUnavailable{Key: s.key, Reason: "language server is not running"}
	}
	params := didOpenParams{}
	params.TextDocument.URI = uri
	params.TextDocument.LanguageID = lspLanguageID(lang)
	params.TextDocument.Version = 1
	params.TextDocument.Text = string(text)
	if err := c.Notify(mDidOpen, params); err != nil {
		return err
	}
	s.docs[rel] = &openDoc{rel: rel, uri: uri, language: lang, version: 1, text: string(text)}
	return nil
}

func (s *Session) change(rel string) {
	s.docMu.Lock()
	doc := s.docs[rel]
	s.docMu.Unlock()
	if doc == nil {
		return
	}
	text, err := s.m.ws.Read(rel)
	if err != nil {
		s.closeDoc(rel)
		return
	}
	c := s.current()
	if c == nil {
		return
	}
	s.docMu.Lock()
	doc = s.docs[rel]
	if doc == nil {
		s.docMu.Unlock()
		return
	}
	if doc.text == string(text) {
		s.docMu.Unlock()
		return
	}
	doc.text = string(text)
	doc.version++
	ver := doc.version
	uri := doc.uri
	s.docMu.Unlock()
	_ = c.Notify(mDidChange, didChangeParams{
		TextDocument: struct {
			URI     string `json:"uri"`
			Version int    `json:"version"`
		}{URI: uri, Version: ver},
		ContentChanges: []struct {
			Text string `json:"text"`
		}{{Text: string(text)}},
	})
}

func (s *Session) closeDoc(rel string) {
	s.docMu.Lock()
	doc := s.docs[rel]
	if doc == nil {
		s.docMu.Unlock()
		return
	}
	delete(s.docs, rel)
	s.docMu.Unlock()
	if c := s.current(); c != nil {
		p := struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}{}
		p.TextDocument.URI = doc.uri
		_ = c.Notify(mDidClose, p)
	}
	s.clearDiagnostics(rel, true)
}

func (s *Session) clearDiagnostics(rel string, emit bool) {
	s.diagMu.Lock()
	delete(s.diags, rel)
	s.diagMu.Unlock()
	if emit {
		s.m.emit(Event{Type: "diagnostics", Diagnostics: &FileDiagnostics{
			Path:   rel,
			Server: s.key,
		}})
	}
}

func (s *Session) restart(ctx context.Context) error {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	s.mu.Lock()
	c := s.client
	s.closing = true
	s.client = nil
	s.status = "stopped"
	s.pid = 0
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
	s.docMu.Lock()
	rels := make([]string, 0, len(s.docs))
	langs := make([]string, 0, len(s.docs))
	for rel, d := range s.docs {
		rels = append(rels, rel)
		langs = append(langs, d.language)
	}
	s.docs = make(map[string]*openDoc)
	s.docMu.Unlock()
	sort.Strings(rels)
	s.diagMu.Lock()
	paths := make([]string, 0, len(s.diags))
	for p := range s.diags {
		paths = append(paths, p)
	}
	s.diags = make(map[string][]Diagnostic)
	s.diagMu.Unlock()
	for _, p := range paths {
		s.m.emit(Event{Type: "diagnostics", Diagnostics: &FileDiagnostics{Path: p, Server: s.key}})
	}
	if _, err := s.startLocked(ctx); err != nil {
		return err
	}
	// Reopen documents. rels/lang indexes line up because both were built in
	// the same map iteration.
	for i, rel := range rels {
		if err := s.open(ctx, rel, langs[i]); err != nil {
			s.clearDiagnostics(rel, true)
		}
	}
	return nil
}

func (s *Session) stop() {
	s.ensureMu.Lock()
	defer s.ensureMu.Unlock()
	s.mu.Lock()
	c := s.client
	s.closing = true
	s.client = nil
	s.status = "stopped"
	s.pid = 0
	s.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

func (s *Session) handleNotify(method string, params json.RawMessage) {
	switch method {
	case mPublishDiagnostics:
		var p publishDiagnosticsParams
		if err := json.Unmarshal(params, &p); err != nil {
			return
		}
		rel, ok := s.m.pathForURI(p.URI)
		if !ok || rel == "" {
			return
		}
		ds := sanitizeDiagnostics(p.Diagnostics)
		s.diagMu.Lock()
		if len(ds) == 0 {
			delete(s.diags, rel)
		} else {
			s.diags[rel] = ds
		}
		s.diagMu.Unlock()
		fd := FileDiagnostics{Path: rel, Server: s.key, Diagnostics: ds}
		for _, d := range ds {
			fd.Counts.add(d.Severity)
		}
		s.m.emit(Event{Type: "diagnostics", Diagnostics: &fd})
	case "window/logMessage", "window/showMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(params, &p) == nil {
			if c := s.current(); c != nil {
				c.AddLog(p.Message)
			}
		}
	}
}

func sanitizeDiagnostics(in []Diagnostic) []Diagnostic {
	const max = 2000
	if len(in) > max {
		in = in[:max]
	}
	out := make([]Diagnostic, 0, len(in))
	for _, d := range in {
		if d.Severity < 0 || d.Severity > 4 {
			d.Severity = 3
		}
		if len(d.Message) > 8192 {
			d.Message = d.Message[:8192] + "…"
		}
		if d.Range.Start.Line < 0 {
			d.Range.Start.Line = 0
		}
		if d.Range.Start.Character < 0 {
			d.Range.Start.Character = 0
		}
		d.Related = nil // keep payloads small; definitions are available via navigation
		out = append(out, d)
	}
	return out
}

func (s *Session) diagnostics() []FileDiagnostics {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	out := make([]FileDiagnostics, 0, len(s.diags))
	for rel, ds := range s.diags {
		if len(ds) == 0 {
			continue
		}
		fd := FileDiagnostics{Path: rel, Server: s.key, Diagnostics: append([]Diagnostic(nil), ds...)}
		for _, d := range ds {
			fd.Counts.add(d.Severity)
		}
		sortDiagnostics(fd.Diagnostics)
		out = append(out, fd)
	}
	return out
}

func (s *Session) diagnosticsFor(rel string) FileDiagnostics {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	ds := append([]Diagnostic{}, s.diags[rel]...)
	fd := FileDiagnostics{Path: rel, Server: s.key, Diagnostics: ds}
	for _, d := range ds {
		fd.Counts.add(d.Severity)
	}
	sortDiagnostics(fd.Diagnostics)
	return fd
}

func sortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Range.Start.Line != b.Range.Start.Line {
			return a.Range.Start.Line < b.Range.Start.Line
		}
		if a.Range.Start.Character != b.Range.Start.Character {
			return a.Range.Start.Character < b.Range.Start.Character
		}
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		return a.Message < b.Message
	})
}

// ---- conversion helpers ----

// PathToURI converts an absolute path to a file URI.
func PathToURI(abs string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
}

// PathFromURI converts a file URI to a path.
func PathFromURI(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", err
	}
	if u.Scheme != "file" {
		return "", fmt.Errorf("not a file URI: %s", uri)
	}
	return filepath.FromSlash(u.Path), nil
}

func lspLanguageID(lang string) string {
	switch lang {
	case "javascriptreact", "typescriptreact":
		return lang
	default:
		return lang
	}
}

func parseLocations(raw json.RawMessage, mapPath func(string) (string, bool)) []NavLocation {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil
		}
	} else {
		items = []json.RawMessage{raw}
	}
	out := make([]NavLocation, 0, len(items))
	seen := make(map[string]bool)
	for _, item := range items {
		var probe struct {
			URI                  string `json:"uri"`
			Range                *Range `json:"range"`
			TargetURI            string `json:"targetUri"`
			TargetRange          *Range `json:"targetRange"`
			TargetSelectionRange *Range `json:"targetSelectionRange"`
		}
		if err := json.Unmarshal(item, &probe); err != nil {
			continue
		}
		uri := probe.URI
		if uri == "" {
			uri = probe.TargetURI
		}
		r := probe.Range
		if r == nil {
			r = probe.TargetSelectionRange
		}
		if r == nil {
			r = probe.TargetRange
		}
		if uri == "" || r == nil {
			continue
		}
		rel, ok := mapPath(uri)
		if !ok || rel == "" {
			continue
		}
		loc := NavLocation{
			Path:      rel,
			StartLine: r.Start.Line,
			StartChar: r.Start.Character,
			EndLine:   r.End.Line,
			EndChar:   r.End.Character,
		}
		key := fmt.Sprintf("%s:%d:%d:%d:%d", loc.Path, loc.StartLine, loc.StartChar, loc.EndLine, loc.EndChar)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, loc)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		if out[i].StartLine != out[j].StartLine {
			return out[i].StartLine < out[j].StartLine
		}
		return out[i].StartChar < out[j].StartChar
	})
	return out
}

func parseHover(raw json.RawMessage) Hover {
	var rh rawHover
	if err := json.Unmarshal(raw, &rh); err != nil || len(rh.Contents) == 0 {
		return Hover{}
	}
	h := Hover{Range: rh.Range}
	var s string
	if json.Unmarshal(rh.Contents, &s) == nil {
		h.Contents = MarkupContent{Kind: "plaintext", Value: s}
		return h
	}
	var mc MarkupContent
	if json.Unmarshal(rh.Contents, &mc) == nil && mc.Value != "" {
		if mc.Kind == "" {
			mc.Kind = "markdown"
		}
		h.Contents = mc
		return h
	}
	var arr []json.RawMessage
	if json.Unmarshal(rh.Contents, &arr) == nil {
		var b strings.Builder
		for _, item := range arr {
			var part string
			if json.Unmarshal(item, &part) == nil {
				b.WriteString(part)
				b.WriteString("\n\n")
				continue
			}
			if json.Unmarshal(item, &mc) == nil {
				b.WriteString(mc.Value)
				b.WriteString("\n\n")
			}
		}
		h.Contents = MarkupContent{Kind: "markdown", Value: strings.TrimSpace(b.String())}
	}
	return h
}

func parseSymbols(raw json.RawMessage, mapPath func(string) (string, bool)) []Symbol {
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	// Determine whether these are DocumentSymbols (selectionRange) or
	// SymbolInformation (location).
	if len(items) > 0 {
		var probe struct {
			SelectionRange *Range    `json:"selectionRange"`
			Location       *Location `json:"location"`
		}
		_ = json.Unmarshal(items[0], &probe)
		if probe.SelectionRange == nil && probe.Location != nil {
			out := make([]Symbol, 0, len(items))
			for _, item := range items {
				var si SymbolInformation
				if err := json.Unmarshal(item, &si); err != nil {
					continue
				}
				if _, ok := mapPath(si.Location.URI); !ok {
					continue
				}
				out = append(out, Symbol{
					Name:      si.Name,
					Kind:      SymbolKind[si.Kind],
					StartLine: si.Location.Range.Start.Line,
					StartChar: si.Location.Range.Start.Character,
					EndLine:   si.Location.Range.End.Line,
					EndChar:   si.Location.Range.End.Character,
				})
			}
			return out
		}
	}
	var ds []DocumentSymbol
	if err := json.Unmarshal(raw, &ds); err != nil {
		return nil
	}
	return convertSymbols(ds, 0)
}

func convertSymbols(ds []DocumentSymbol, depth int) []Symbol {
	if depth > 8 || len(ds) > 5000 {
		return nil
	}
	out := make([]Symbol, 0, len(ds))
	for _, d := range ds {
		kind := SymbolKind[d.Kind]
		out = append(out, Symbol{
			Name:      d.Name,
			Detail:    d.Detail,
			Kind:      kind,
			StartLine: d.Range.Start.Line,
			StartChar: d.Range.Start.Character,
			EndLine:   d.Range.End.Line,
			EndChar:   d.Range.End.Character,
			Children:  convertSymbols(d.Children, depth+1),
		})
	}
	return out
}
