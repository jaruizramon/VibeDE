// Package apphost owns the workspace HTTP server lifecycle. The native desktop
// window embeds this server; switching workspaces starts a fresh server and
// retires the old one without exposing a browser tab or a public port.
package apphost

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"vibede/internal/config"
	"vibede/internal/server"
)

// Options controls how the internal listener is created.
type Options struct {
	ListenHost   string // default 127.0.0.1
	URLHost      string // host shown in URLs; defaults to ListenHost or loopback
	Port         int    // 0 lets the kernel choose
	NoServers    bool
	WindowOpener func(url, title string) error
}

// Manager starts and swaps workspace servers.
type Manager struct {
	mu      sync.Mutex
	opts    Options
	current *instance
}

type instance struct {
	root string
	url  string
	srv  *server.Server
	http *http.Server
	ln   net.Listener
	once sync.Once
}

// New creates a manager. It does not start a workspace.
func New(opts Options) *Manager {
	if opts.ListenHost == "" {
		opts.ListenHost = "127.0.0.1"
	}
	return &Manager{opts: opts}
}

// Start launches the initial workspace server.
func (m *Manager) Start(root string) (string, error) {
	inst, err := m.start(root)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	old := m.current
	m.current = inst
	m.mu.Unlock()
	if old != nil {
		go old.close()
	}
	return inst.url, nil
}

// Switch starts a server for a new root and retires the previous one.
func (m *Manager) Switch(root string) (string, error) {
	inst, err := m.start(root)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	old := m.current
	m.current = inst
	m.mu.Unlock()
	if old != nil {
		go old.close()
	}
	return inst.url, nil
}

// Root returns the currently served workspace root.
func (m *Manager) Root() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return ""
	}
	return m.current.root
}

// Close stops the current workspace server.
func (m *Manager) Close() {
	m.mu.Lock()
	inst := m.current
	m.current = nil
	m.mu.Unlock()
	if inst != nil {
		inst.close()
	}
}

func (m *Manager) start(root string) (*instance, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	cfg.NoServers = cfg.NoServers || m.opts.NoServers
	addr := net.JoinHostPort(m.opts.ListenHost, strconv.Itoa(m.opts.Port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	srv, err := server.New(cfg)
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	if m.opts.WindowOpener != nil {
		srv.SetWindowOpener(m.opts.WindowOpener)
	}
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		_ = httpSrv.Serve(ln)
	}()
	urlHost := m.opts.URLHost
	if urlHost == "" {
		urlHost = m.opts.ListenHost
	}
	if urlHost == "0.0.0.0" || urlHost == "::" || urlHost == "" {
		urlHost = "127.0.0.1"
	}
	port := 0
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok {
		port = tcp.Port
	}
	url := "http://" + net.JoinHostPort(urlHost, strconv.Itoa(port)) + "/"
	return &instance{
		root: cfg.Root,
		url:  url,
		srv:  srv,
		http: httpSrv,
		ln:   ln,
	}, nil
}

func (i *instance) close() {
	i.once.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := i.http.Shutdown(ctx)
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			_ = i.http.Close()
		}
		i.srv.Close()
		_ = i.ln.Close()
	})
}
