# VibeDE

VibeDE is a **read-only IDE for vibecoding**. It is built for engineers who need
to audit, trace and understand what an agent is changing while it changes it:

- **Live agent edits** — every filesystem write is decoded into line diffs and
  streamed to an activity feed.
- **Code flow** — `Ctrl/⌘ + click` goes to a definition and `Alt + click` finds
  references through real language servers.
- **Structure and cost** — per-function structure, cyclomatic complexity and a
  static **Big-O estimate with evidence lines**.
- **Deterministic diagrams** — control flow, UML types, use cases, call
  sequences, imports and HTML DOM trees, rendered as SVG (plus DOT and Mermaid
  exports).
- **Errors and warnings** — gopls, Pyright, TypeScript, HTML/CSS/JSON, clangd
  and rust-analyzer publish diagnostics over LSP.
- **Selection explainer** — select code, ask a question in the popover, and a
  local small model answers using the file and the files it imports. `Ctrl/⌘ + E`
  opens it from the keyboard; `Enter` with an empty field means “explain this”.
- **History in the Activity feed** — the same feed that shows recent edits also
  shows each answer, and both are persisted across restarts in one
  gzip-compressed `.vibede.rab` file.
- **Inline line notes** — answers are requested as line-anchored notes
  (`- **Line 125**: …`). Click an explanation in Activity (or “Show N line
  notes” in the popover) and the notes render above the lines they describe,
  without changing the file's line numbering; each note can be dismissed.

VibeDE cannot edit your source files. There is no general write endpoint, and
language servers' `workspace/applyEdit` requests are explicitly refused. The
only file VibeDE writes is its own `.vibede.rab` history archive in the
workspace root (see below); it is ignored by the watcher and explorer.

## Quick start

Requirements: Go 1.23+, GTK3 + WebKitGTK 4.1 runtime libraries, a graphical
display, and at least one language server (see below).

```bash
make build
./bin/vibede
```

VibeDE opens a **native desktop window**. With no `--root`, it first shows a
folder chooser. The HTML/Go-WASM interface is embedded in the window through
GTK/WebKit; there is no browser tab and no public URL.

```bash
./bin/vibede --root /path/to/code          # skip the initial chooser
./bin/vibede --ui browser --root /path/code  # optional external browser
./bin/vibede --ui headless --root /path/code # serve only; print the URL
```

The native window keeps its HTTP server on a kernel-allocated loopback port.
Use the native **Open Folder…** button to switch workspace at runtime; a fresh
server is started for the new root and the old one is retired. `--port` and a
non-loopback `--host` only affect `--ui browser`/`--ui headless`.

The binary is built with `CGO_ENABLED=0`. GTK and WebKit are called through
`purego` at runtime, so the application itself remains pure Go (no cgo bridge,
no Node build step).

### CLI

| Flag | Meaning |
|---|---|
| `--ui native\|browser\|headless` | Window mode (default `native`). |
| `--root DIR` | Workspace to inspect; native mode prompts when omitted. |
| `--host HOST` | Listen host for browser/headless (default `127.0.0.1`). |
| `--port N` | Listen port for browser/headless; `0` chooses. |
| `--open` | Open in the external browser (same as `--ui browser`). |
| `--no-servers` | Disable all language servers. |
| `--version` | Print the version. |

## UI

The window has a small native GTK bar with **Open Folder…** and the current
workspace path, followed by the embedded VibeDE interface.

- **Explorer** with directory expansion and filename filtering (`Ctrl/⌘ + K`
  or `Ctrl/⌘ + P`).
- **Code search** (`Ctrl/⌘ + F`; alias `Ctrl/⌘ + Shift + F`): searches every
  text file in the workspace, with an **Aa** case-sensitive toggle, line
  previews and match counts. Clicking a result opens the file at that line and
  highlights it.
- **Workspace chooser** on launch (when `--root` is omitted) and from the
  native **Open Folder…** button; switching roots reloads the interface.
- **Empty workspace** shows the selected root and points to **Open Folder…**.
- **Structure** outline with Big-O and cyclomatic-complexity badges.
- **Editor** is a read-only syntax-highlighted viewer. Clicking a Big-O badge
  or outline row opens the evidence in **Inspector**.
- **Split editor:** use **Split** in either pane's toolbar. Each pane has its
  own tabs, active file, scroll position and focus. Drag the divider between
  panes to rebalance them.
- **Resizable right panel:** drag the divider before Activity/Problems/
  Diagrams/Inspector. The code editor and diagram panel can each be given the
  width they need.
- **Detach:** open the active file in its own native window with **Detach**
  (`⧉`). The detached window is code-focused; the sidebar and right panel are
  hidden. Detach is unavailable in browser/headless mode.
- **Activity** shows each agent write with added/removed counts and a diff.
  Clicking an item opens the file at the **first changed line** and highlights
  added/deleted lines; open files refresh automatically after writes.
- **Problems** groups language-server diagnostics; clicking an item jumps to
  the exact line.
- **Diagrams** builds a deterministic SVG and copies DOT/Mermaid source. UML
  includes cross-file type relationships, not just boxes. Drag the right-panel
  divider or use **− / + / Fit / 100%** to scale the diagram.
- **Language servers** shows detected, running, failed and missing servers
  with install hints and restart controls.

Keyboard/mouse:

| Input | Action |
|---|---|
| `Ctrl/⌘ + click` | Go to definition |
| `Alt + click` | Find references |
| `Ctrl/⌘ + F` | Focus workspace code search (alias `Ctrl/⌘ + Shift + F`) |
| `Ctrl/⌘ + E` | Ask the small model about the selected code |
| `Ctrl/⌘ + K` or `Ctrl/⌘ + P` | Focus file filter |
| `Ctrl/⌘ + W` | Close the active tab |
| `Ctrl/⌘ + \` | Toggle split editor |
| `Ctrl/⌘ + PageDown` / `PageUp` | Next / previous tab |
| `Alt + 1` … `Alt + 5` | Activity, Problems, Search, Diagrams, Inspector |
| `F1` | Open help |
| `Esc` | Close help |
| **⌕ ◇ ! ◫ ⧉** | Inspector, diagrams, problems, split toggle, detach (icons appear in split mode; hover for labels) |
| Toolbar **⌕ ◇ ! ◫ ⧉** | Inspector, diagrams, problems, split toggle, detach (icons appear in split mode; hover for labels) |

## Language servers

VibeDE detects servers on `PATH`, `~/go/bin`, `~/.local/bin`, `~/.cargo/bin`,
Node's install directory, npm's global prefix and `/usr/local/bin`. Server
groups and default launch commands:

| Language | Server | Install |
|---|---|---|
| Go | `gopls` | `go install golang.org/x/tools/gopls@latest` |
| Python | `pyright-langserver --stdio` (fallback `pylsp`) | `npm i -g pyright` or `pip install python-lsp-server` |
| JavaScript / TypeScript | `typescript-language-server --stdio` | `npm i -g typescript@5 typescript-language-server` |
| HTML | `vscode-html-language-server --stdio` | `npm i -g vscode-langservers-extracted` |
| CSS / SCSS / LESS | `vscode-css-language-server --stdio` | `npm i -g vscode-langservers-extracted` |
| JSON | `vscode-json-language-server --stdio` | `npm i -g vscode-langservers-extracted` |
| C / C++ | `clangd` | install clangd (LLVM) |
| Rust | `rust-analyzer` | `rustup component add rust-analyzer` |
| Java (optional) | `jdtls` | install eclipse.jdt.ls |
| YAML (optional) | `yaml-language-server --stdio` | `npm i -g yaml-language-server` |
| Markdown (optional) | `vscode-markdown-language-server --stdio` | `npm i -g vscode-langservers-extracted` |
| Lua (optional) | `lua-language-server` | install LuaLS |
| Shell (optional) | `bash-language-server start` | `npm i -g bash-language-server` |

The TypeScript server needs a compatible TypeScript 5.x installation. VibeDE
finds a workspace `node_modules/typescript/lib/tsserver.js` first, then a
global installation.

The vscode-html language service validates embedded CSS/JS, not HTML syntax
itself. Missing HTML-syntax diagnostics are an upstream limitation, not a
silent failure.

A missing server never blocks reading code; VibeDE still shows structure,
complexity, Big-O and diagrams from its own analyzers, and displays the install
hint in the server panel.

## Big-O and complexity

Each named function/method gets:

- **Cyclomatic complexity**: `1 + if + for + range + non-default case +
  select/comm clause + && + ||`.
- **Big-O**: a static estimate composed from:
  - nested loop products and sequential-loop maxima;
  - halving/doubling loops and binary-search `mid` patterns (`O(log n)`);
  - constant-bounded loops (`O(1)`) and collection ranges (`O(n)`);
  - known library costs (`sort.Slice`/`slices.Sort`/`.sort()` →
    `O(n log n)`, `sort.Search` → `O(log n)`, joins/splits/reads → `O(n)`);
  - same-file helper composition (a helper called inside a loop multiplies);
  - recursion shapes: single halving → `O(log n)`, two halving calls →
    `O(n log n)`, overlapping decrementing calls → `O(2^n)`, linear recursion →
    `O(n)`;
  - tree traversal: recursion inside a loop over `.children`, `.childNodes`,
    `.next`, `.left` or `.right` visits each node once → `O(n)`, instead of
    multiplying the recursion by the loop.
- **Evidence**: deterministic strings such as
  `nested loop at line 24 → O(n)` or
  `sort call at line 48 → O(n log n)`.

Big-O is an estimate, not a measurement. Cross-file/cross-package calls and
unknown external methods are treated as `O(1)` unless a known standard-library
cost matches. Calls whose arguments are all literals/composite literals do not
inherit a helper's input-dependent cost (`fib(6)` is constant work for its
caller). The UI always labels the estimate as static.

## Deterministic diagrams

Diagrams are generated from Go data structures; no client-side diagram library
is used. The same source produces byte-identical SVG/DOT/Mermaid output, which
is covered by tests.

| Diagram | Availability |
|---|---|
| **Flowchart** | Go control-flow graph per function; a call map when no function is selected. Other languages show their call map with a note. |
| **UML** | Package/directory scope: structs/classes/interfaces/enums/records with fields and methods, plus inheritance, embedding, interface implementation, field composition and type-usage dependencies across sibling files. |
| **Use cases** | HTTP routes detected in the current file for Go (`ServeMux`/chi/gin-style calls), Python decorators, Express-style JS/TS and Rust `.route(...)`; falls back to entrypoints. |
| **Sequence** | Call chain from the selected root, depth 2, including returns and self-recursion. |
| **Imports** | Current file → imported modules/packages. |
| **DOM** | HTML element tree with ids/classes and short text. |

Layout is a deterministic layered graph layout with back-edge handling,
barycentric ordering, fixed text metrics and stable tie-breaking. Export buttons
give you DOT and Mermaid source; SVG is rendered by VibeDE's own Go renderer.

## HTTP API

All content endpoints are read-only. POST endpoints are queries or server
lifecycle actions; none write workspace files. `/api/explain` is the only route
that sends workspace content off-process — to the endpoint configured in
`ai.endpoint`, which defaults to loopback.

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/info` | Version, root, limits, server status, watcher stats |
| GET | `/api/tree?path=` | Directory listing |
| GET | `/api/file?path=` | Text content; rejects binary/oversized/escaping paths |
| GET | `/api/analysis?path=` | Structure, complexity, Big-O, metrics |
| GET | `/api/diagnostics?path=` | Diagnostics for one file, or all when omitted |
| GET | `/api/servers` | Language-server status and logs |
| GET | `/api/symbols?path=` | LSP document symbols |
| GET | `/api/diagram?path=&kind=&function=` | Rendered SVG + DOT/Mermaid |
| GET | `/api/search?q=` | Filename/path search |
| GET | `/api/search/code?q=&case=1` | Case-sensitive/insensitive text search with file/line matches |
| GET | `/api/events` | SSE: `ready`, `change`, `diagnostics`, `server` |
| GET | `/api/archive` | Decoded `.vibede.rab` history: deltas and explanations |
| POST | `/api/lsp/open` | Start a server and sync a document |
| POST | `/api/lsp/close` | Stop syncing a document |
| POST | `/api/navigate` | Definition/references/implementation/type definition |
| POST | `/api/hover` | LSP hover |
| POST | `/api/explain` | Ask the configured SLM about a selection; sends the selection, its file and linked imports |
| POST | `/api/servers/restart` | Restart one server and reopen documents |
| POST | `/api/servers/rescan` | Re-detect binaries |

Paths are always workspace-relative. Absolute paths, `..` traversal and
symlinks escaping the root are rejected.

## Configuration

Optional `.vibede.json` in the workspace root (read-only; VibeDE never writes
it):

```json
{
  "ignore": ["generated", "*.min.js"],
  "limits": {
    "maxFileBytes": 8388608,
    "maxSnapshotBytes": 1048576,
    "snapshotBudgetBytes": 268435456,
    "maxDiagramNodes": 400,
    "maxRenderedLines": 12000
  },
  "watch": { "debounceMs": 120 },
  "noServers": false,
  "ai": {
    "endpoint": "http://127.0.0.1:8080/v1",
    "model": "qwen3.8-27b-q4-k-m",
    "maxTokens": 1200,
    "timeoutMs": 120000,
    "thinking": false
  },
  "servers": {
    "typescript": {
      "command": "/opt/node/bin/typescript-language-server",
      "args": ["--stdio"]
    },
    "python": { "command": "pylsp" },
    "c": { "disabled": true }
  }
}
```

`command` must be a name on the search path or an absolute path; `args`
replace the defaults. `.git`, `node_modules`, virtualenvs, build outputs and
similar noise directories are ignored by default.

### Selection explanations

`ai.endpoint` is any OpenAI-compatible base URL (llama.cpp, vLLM, LM Studio,
or a hosted API). It defaults to `http://127.0.0.1:8080/v1` — llama.cpp's
conventional address — so a local model works without config. When `model` is
empty, VibeDE asks the endpoint for `/models` and uses the first id. The API key
is optional and is only sent to that endpoint.

Flags override the file; `VIBEDE_AI_ENDPOINT`, `VIBEDE_AI_MODEL` and
`VIBEDE_AI_KEY` override the file too and are overridden by flags:

```bash
./bin/vibede --ai-url http://127.0.0.1:8080/v1 --ai-model qwen3.8-27b-q4-k-m
./bin/vibede --no-ai
```

The request contains the selected snippet, the selection's file and up to four
workspace files its imports resolve to (relative imports, package imports via
`go.mod`, Python relative imports). Files outside the workspace are never read.
Imports to the standard library or `node_modules` are skipped. `thinking: false`
sets `chat_template_kwargs.enable_thinking = false` for reasoning models;
enable it if you want the model to reason first.

Each explanation is capped at **22,000 bytes of context** in total — selection
plus file excerpts. That context is discarded as soon as the answer is written;
only the question, answer, model name and the names of the files used are kept.

The system prompt requires the answer to be a flat list of line-anchored notes:

```
- **Line 125**: what this line does
- **Line 126-128**: what this range does together
```

The server parses those anchors (`internal/server/explain.go`) and stores them
with the answer. Clicking an explanation card in Activity — or the “Show N line
notes” button in the popover — renders each note as a comment-like block above
its line. Notes occupy layout space but no line number, so the file's numbering
does not shift; the `×` on a note removes it. Prose answers that do not follow
the format still display in the popover and Activity, but place no inline notes.

The full history lives in `.vibede.rab` in the workspace root:

- a single gzip-compressed JSON document (version, update time, the newest 200
  edit deltas and the newest 200 explanations with their parsed line notes), so
  the archive is compressed “inside itself” and is self-contained;
- written atomically (`.vibede.rab.tmp` then rename) at most once per second;
- ignored by the watcher, the explorer and `git` (the repo's `.gitignore`
  covers it), so it never shows up as an agent edit;
- corrupt or missing archives are replaced on the next save.

`GET /api/archive` returns the decoded history; the Activity panel merges it
with the live edit stream and shows explanation cards above or below edits by
timestamp.

## Architecture

```
cmd/vibede            CLI + native window / headless host
cmd/vibede-web        HTML UI logic, Go compiled to WebAssembly
internal/apphost      Workspace server lifecycle and runtime switching
internal/desktop      Native GTK3 + WebKitGTK window via purego (no cgo)
internal/workspace    Root sandbox, file tree, language detection
internal/watch        Recursive fsnotify watcher + bounded content snapshots
internal/diff         Myers line diff with a bounded fallback
internal/lsp          JSON-RPC/LSP client, server registry, diagnostics, nav
internal/analyze      Go AST + generic lexers, complexity, Big-O, diagram builders
internal/diagram      Graph model, deterministic layout, SVG/DOT/Mermaid renderers
internal/highlight    Shared syntax tokenizer for the viewer
internal/server       HTTP API (sources read-only) with .rab archive, SSE broker
internal/config       `.vibede.json` loading and defaults
internal/rab          gzip-compressed .rab history: deltas + explanations
```

The interface logic is written in Go and compiled to WebAssembly.
`index.html` and `style.css` are the shell, WebKit renders them inside the
native window, and `wasm_exec.js` is the runtime shipped with the Go toolchain.
There is no JavaScript application framework and no Node build step.

The native window and workspace server are both Go. GTK/WebKit are loaded as
system shared libraries through `purego`; the workspace switcher starts a new
loopback server on an ephemeral port and loads its URL in the embedded view.
When native GTK/WebKit is unavailable, `--ui browser` and `--ui headless` use
the same internal server directly.

Go dependencies are `purego` and `fsnotify` (plus `x/sys`).

## Build and test

```bash
make build   # WebAssembly assets + cgo-free native bin/vibede
make test    # build assets, then go test ./...
make run     # build and open the native window on the current directory
make clean
```

`make web` alone rebuilds the embedded client. `go build ./...` also succeeds
(the web package has a non-wasm stub), but the embedded UI from a fresh
checkout must be produced with `make web`/`make build` before starting VibeDE.

Native mode needs the GTK3 and WebKitGTK 4.1 runtime packages (for example
`libgtk-3-0` and `libwebkit2gtk-4.1-0` on Debian/Ubuntu). No development
headers are required because there is no cgo compilation.

## Read-only guarantees and security

- No endpoint writes, renames, deletes, formats or applies code actions.
- Every language-server `workspace/applyEdit` is answered with
  `applied: false` and a failure reason.
- LSP registration is limited to navigation, hover, symbols and diagnostics.
- Paths are resolved and symlink-checked against the workspace root.
- The native window's internal listener is loopback-only and ephemeral.
- `--ui browser`/`--ui headless` default to loopback; there is no
  authentication, so a non-loopback host is explicitly warned about.
- Reading an empty directory produces an explicit empty-workspace screen; it
  does not invent files or fall back to another root.
- Content Security Policy allows only same-origin assets and WebAssembly.
- Bounded snapshots (default 256 MiB total, 1 MiB per file) keep the memory
  cost predictable; larger files are still viewable but not diff-snapshotted.
- Language servers may maintain their own caches outside the workspace; VibeDE
  itself never writes into it.

## Known limitations

- Big-O and complexity are static estimates; dynamic data shapes, macros and
  cross-package behavior are not modeled.
- Generic-language flowcharts show a call map; control-flow extraction is
  implemented for Go.
- UML diagrams span the current Go package or directory; use-case and import
  diagrams describe the current file.
- HTML servers do not report HTML syntax errors (embedded CSS/JS only).
- Java, YAML, Lua and shell support is registry-driven and depends on optional
  external servers.
- The read-only viewer caps rendered lines (12,000 by default); analysis and
  diagnostics still cover the whole file.
- The GUI is a native Go window embedding WebKit; it intentionally uses HTML
  for the interface instead of a native widget toolkit. Split panes and
  detached windows are native window/editor-group features.
- A detached window is tied to the server instance that created it; switching
  workspace in the main window retires that server, so close detached windows
  first if you intend to switch roots.
- Native mode requires the GTK/WebKit runtime; otherwise use `--ui browser` or
  `--ui headless`.
