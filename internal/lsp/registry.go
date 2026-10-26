package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"vibede/internal/config"
)

type candidate struct {
	Command string
	Args    []string
}

type serverSpec struct {
	Key        string
	Name       string
	Languages  []string
	Install    string
	Docs       string
	Candidates []candidate
}

// registry is ordered. The order is the display order in the UI.
var registry = []serverSpec{
	{
		Key: "go", Name: "gopls", Languages: []string{"go"},
		Install:    "go install golang.org/x/tools/gopls@latest",
		Docs:       "https://pkg.go.dev/golang.org/x/tools/gopls",
		Candidates: []candidate{{Command: "gopls"}},
	},
	{
		Key: "python", Name: "Pyright / Jedi", Languages: []string{"python"},
		Install: "npm i -g pyright   (or pip install python-lsp-server)",
		Docs:    "https://microsoft.github.io/pyright/",
		Candidates: []candidate{
			{Command: "pyright-langserver", Args: []string{"--stdio"}},
			{Command: "pylsp"},
		},
	},
	{
		Key: "typescript", Name: "TypeScript Language Server",
		Languages: []string{"javascript", "javascriptreact", "typescript", "typescriptreact"},
		Install:   "npm i -g typescript@5 typescript-language-server",
		Docs:      "https://github.com/typescript-language-server/typescript-language-server",
		Candidates: []candidate{
			{Command: "typescript-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Key: "html", Name: "HTML Language Server", Languages: []string{"html"},
		Install: "npm i -g vscode-langservers-extracted",
		Docs:    "https://github.com/hrsh7th/vscode-langservers-extracted",
		Candidates: []candidate{
			{Command: "vscode-html-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Key: "css", Name: "CSS Language Server", Languages: []string{"css", "scss", "less"},
		Install: "npm i -g vscode-langservers-extracted",
		Docs:    "https://github.com/hrsh7th/vscode-langservers-extracted",
		Candidates: []candidate{
			{Command: "vscode-css-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Key: "json", Name: "JSON Language Server", Languages: []string{"json", "jsonc"},
		Install: "npm i -g vscode-langservers-extracted",
		Docs:    "https://github.com/hrsh7th/vscode-langservers-extracted",
		Candidates: []candidate{
			{Command: "vscode-json-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Key: "c", Name: "clangd", Languages: []string{"c", "cpp"},
		Install: "apt install clangd   (or install LLVM)",
		Docs:    "https://clangd.llvm.org/",
		Candidates: []candidate{
			{Command: "clangd"},
			{Command: "clangd-18"},
			{Command: "clangd-17"},
		},
	},
	{
		Key: "rust", Name: "rust-analyzer", Languages: []string{"rust"},
		Install: "rustup component add rust-analyzer",
		Docs:    "https://rust-analyzer.github.io/",
		Candidates: []candidate{
			{Command: "rust-analyzer"},
		},
	},
	{
		Key: "java", Name: "Eclipse JDT Language Server", Languages: []string{"java"},
		Install: "install eclipse.jdt.ls and put jdtls on PATH",
		Docs:    "https://github.com/eclipse-jdtls/eclipse.jdt.ls",
		Candidates: []candidate{
			{Command: "jdtls"},
		},
	},
	{
		Key: "yaml", Name: "YAML Language Server", Languages: []string{"yaml"},
		Install: "npm i -g yaml-language-server",
		Docs:    "https://github.com/redhat-developer/yaml-language-server",
		Candidates: []candidate{
			{Command: "yaml-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Key: "markdown", Name: "Markdown Language Server", Languages: []string{"markdown"},
		Install: "npm i -g vscode-langservers-extracted",
		Docs:    "https://github.com/hrsh7th/vscode-langservers-extracted",
		Candidates: []candidate{
			{Command: "vscode-markdown-language-server", Args: []string{"--stdio"}},
		},
	},
	{
		Key: "lua", Name: "Lua Language Server", Languages: []string{"lua"},
		Install: "install lua-language-server",
		Docs:    "https://luals.github.io/",
		Candidates: []candidate{
			{Command: "lua-language-server"},
		},
	},
	{
		Key: "shell", Name: "Bash Language Server", Languages: []string{"shell"},
		Install: "npm i -g bash-language-server",
		Docs:    "https://github.com/bash-lsp/bash-language-server",
		Candidates: []candidate{
			{Command: "bash-language-server", Args: []string{"start"}},
		},
	},
}

var registryByKey = func() map[string]serverSpec {
	m := make(map[string]serverSpec, len(registry))
	for _, s := range registry {
		m[s.Key] = s
	}
	return m
}()

// resolvedSpec is a registry entry plus the concrete launch decision.
type resolvedSpec struct {
	Spec        serverSpec
	Command     string // absolute path when found
	Args        []string
	InitOptions map[string]any
	Installed   bool
	Disabled    bool
	Source      string // "config" or "path"
	Err         string
}

// searchDirs returns every directory where language-server binaries commonly
// live. Node's directory is added because npm-installed servers live beside
// the node binary.
func searchDirs() []string {
	seen := map[string]bool{}
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		d = filepath.Clean(d)
		if seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	if p := os.Getenv("PATH"); p != "" {
		for _, d := range filepath.SplitList(p) {
			add(d)
		}
	}
	home, _ := os.UserHomeDir()
	add(os.Getenv("GOBIN"))
	if gp := os.Getenv("GOPATH"); gp != "" {
		for _, d := range filepath.SplitList(gp) {
			add(filepath.Join(d, "bin"))
		}
	}
	if home != "" {
		add(filepath.Join(home, "go", "bin"))
		add(filepath.Join(home, ".cargo", "bin"))
		add(filepath.Join(home, ".local", "bin"))
		add(filepath.Join(home, ".npm-global", "bin"))
	}
	add("/usr/local/bin")
	add("/usr/local/go/bin")
	add("/snap/bin")
	if node, err := exec.LookPath("node"); err == nil {
		if resolved, err := filepath.EvalSymlinks(node); err == nil {
			add(filepath.Dir(resolved))
		}
		add(filepath.Dir(node))
	}
	if npmLookup("npm") != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(ctx, "npm", "prefix", "-g").Output(); err == nil {
			add(filepath.Join(strings.TrimSpace(string(out)), "bin"))
		}
	}
	return dirs
}

func npmLookup(name string) string {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func findExecutable(name string, dirs []string) string {
	if name == "" {
		return ""
	}
	if strings.ContainsRune(name, os.PathSeparator) {
		if !filepath.IsAbs(name) {
			return ""
		}
		if executable(name) {
			return name
		}
		return ""
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, dir := range dirs {
		p := filepath.Join(dir, name)
		if executable(p) {
			return p
		}
	}
	return ""
}

func executable(p string) bool {
	st, err := os.Stat(p)
	if err != nil || st.IsDir() {
		return false
	}
	return st.Mode()&0o111 != 0
}

// resolve decides how each server will be launched. It never starts anything.
func resolve(dirs []string, overrides map[string]config.ServerOverride) map[string]*resolvedSpec {
	out := make(map[string]*resolvedSpec, len(registry))
	for _, spec := range registry {
		rs := &resolvedSpec{Spec: spec}
		if ov, ok := overrides[spec.Key]; ok {
			if ov.Disabled {
				rs.Disabled = true
				out[spec.Key] = rs
				continue
			}
			if ov.Command != "" {
				rs.Source = "config"
				cmd := ov.Command
				if !filepath.IsAbs(cmd) && strings.ContainsRune(cmd, os.PathSeparator) {
					cmd = filepath.Clean(cmd)
				}
				rs.Command = findExecutable(cmd, dirs)
				rs.Args = append([]string(nil), ov.Args...)
				if rs.Command == "" {
					rs.Err = "configured command not found: " + ov.Command
				} else {
					rs.Installed = true
				}
				out[spec.Key] = rs
				continue
			}
		}
		for _, cand := range spec.Candidates {
			if path := findExecutable(cand.Command, dirs); path != "" {
				rs.Command = path
				rs.Args = append([]string(nil), cand.Args...)
				rs.Installed = true
				rs.Source = "path"
				break
			}
		}
		if !rs.Installed {
			rs.Command = spec.Candidates[0].Command
			rs.Args = append([]string(nil), spec.Candidates[0].Args...)
			rs.Err = spec.Candidates[0].Command + " not found"
		}
		out[spec.Key] = rs
	}
	return out
}

// envFor returns the child environment with the augmented path first.
func envFor(dirs, nodePaths []string) []string {
	env := []string{"PATH=" + strings.Join(dirs, string(os.PathListSeparator))}
	if len(nodePaths) > 0 {
		env = append(env, "NODE_PATH="+strings.Join(nodePaths, string(os.PathListSeparator)))
	}
	return env
}

// nodeModuleDirs returns locations where Node resolves globally installed
// language servers and their peers (notably the TypeScript compiler).
func nodeModuleDirs() []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		if d == "" {
			return
		}
		d = filepath.Clean(d)
		if seen[d] {
			return
		}
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			return
		}
		seen[d] = true
		out = append(out, d)
	}
	if node, err := exec.LookPath("node"); err == nil {
		if resolved, err := filepath.EvalSymlinks(node); err == nil {
			root := filepath.Dir(filepath.Dir(resolved))
			add(filepath.Join(root, "lib", "node_modules"))
			add(filepath.Join(root, "node_modules"))
		}
	}
	if npm := npmLookup("npm"); npm != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if outBytes, err := exec.CommandContext(ctx, npm, "root", "-g").Output(); err == nil {
			add(strings.TrimSpace(string(outBytes)))
		}
	}
	return out
}

// tsserverPath finds a commonly installed TypeScript server.
func tsserverPath(nodePaths []string, root string) string {
	if root != "" {
		p := filepath.Join(root, "node_modules", "typescript", "lib", "tsserver.js")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, base := range nodePaths {
		p := filepath.Join(base, "typescript", "lib", "tsserver.js")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// specKeys returns registry keys in display order.
func specKeys() []string {
	keys := make([]string, len(registry))
	for i, s := range registry {
		keys[i] = s.Key
	}
	return keys
}
