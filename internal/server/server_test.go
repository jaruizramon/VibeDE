package server

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vibede/internal/config"
	"vibede/internal/watch"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, data string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("main.go", `package main

import "fmt"

type Greeter struct{ Name string }

func greet(n int) {
	for i := 0; i < n; i++ {
		fmt.Println("hi")
	}
}

func main() {
	greet(3)
}
`)
	write("readme.md", "# Demo\n\nLive edits land here.\n")
	write("web/index.html", "<div id=\"app\">hello</div>\n")
	write("routes.go", `package main

import "net/http"

func register(mux *http.ServeMux) {
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {})
}
`)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.NoServers = true
	cfg.Watch.DebounceMS = 30
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	return s, ts, root
}

func get(t *testing.T, ts *httptest.Server, path string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func TestAPISurfaceReadOnly(t *testing.T) {
	_, ts, _ := newTestServer(t)

	resp, body := get(t, ts, "/api/info")
	if resp.StatusCode != 200 {
		t.Fatalf("info status = %d: %s", resp.StatusCode, body)
	}
	var info Info
	if err := json.Unmarshal(body, &info); err != nil {
		t.Fatal(err)
	}
	if !info.ReadOnly || info.Version == "" || info.Root == "" {
		t.Fatalf("info = %+v", info)
	}
	for _, srv := range info.Servers {
		if srv.Status != "disabled" {
			t.Fatalf("NoServers should disable all servers, got %+v", srv)
		}
	}

	resp, body = get(t, ts, "/api/tree?path=")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "main.go") {
		t.Fatalf("tree = %d %s", resp.StatusCode, body)
	}

	resp, body = get(t, ts, "/api/file?path=main.go")
	if resp.StatusCode != 200 {
		t.Fatalf("file = %d %s", resp.StatusCode, body)
	}
	var f fileResponse
	if err := json.Unmarshal(body, &f); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Content, "func greet") || f.Language != "go" {
		t.Fatalf("file = %+v", f)
	}

	resp, body = get(t, ts, "/api/analysis?path=main.go")
	if resp.StatusCode != 200 {
		t.Fatalf("analysis = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"bigO":"O(n)"`) || !strings.Contains(string(body), `"dominantBigO":"O(n)"`) {
		t.Fatalf("analysis body = %s", body)
	}

	resp, body = get(t, ts, "/api/diagram?path=main.go&kind=flowchart&function=greet")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "<svg") {
		t.Fatalf("flowchart = %d %.300s", resp.StatusCode, body)
	}
	resp, body = get(t, ts, "/api/diagram?path=main.go&kind=class")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Greeter") {
		t.Fatalf("class = %d %.300s", resp.StatusCode, body)
	}
	resp, body = get(t, ts, "/api/diagram?path=main.go&kind=usecase")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "main") {
		t.Fatalf("usecase = %d %.300s", resp.StatusCode, body)
	}
	resp, body = get(t, ts, "/api/diagram?path=routes.go&kind=usecase")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "GET /health") {
		t.Fatalf("route usecase = %d %.300s", resp.StatusCode, body)
	}
	resp, body = get(t, ts, "/api/diagram?path=web/index.html&kind=html")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "DOM") {
		t.Fatalf("html diagram = %d %.300s", resp.StatusCode, body)
	}

	resp, body = get(t, ts, "/api/search?q=readme")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "readme.md") {
		t.Fatalf("search = %d %s", resp.StatusCode, body)
	}

	// There is no write surface.
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		req, _ := http.NewRequest(method, ts.URL+"/api/file?path=main.go", strings.NewReader("evil"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s /api/file = %d, want 405", method, resp.StatusCode)
		}
	}

	// Query endpoints reject a missing language server rather than writing.
	req, _ := http.NewRequest("POST", ts.URL+"/api/navigate", strings.NewReader(`{"path":"main.go","line":0,"character":0}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(string(body), "disabled") {
		t.Fatalf("navigate status = %d %s", resp.StatusCode, body)
	}
}

func TestPathTraversalBlocked(t *testing.T) {
	_, ts, _ := newTestServer(t)
	for _, p := range []string{"../etc/passwd", "/etc/passwd", "a/../../etc/passwd"} {
		resp, body := get(t, ts, "/api/file?path="+urlQuery(p))
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%q = %d %s", p, resp.StatusCode, body)
		}
	}
}

func urlQuery(s string) string {
	r := strings.NewReplacer("/", "%2F", "..", "..")
	return r.Replace(s)
}

func TestStaticAssetsAndCSP(t *testing.T) {
	_, ts, _ := newTestServer(t)
	resp, body := get(t, ts, "/")
	if resp.StatusCode != 200 || !strings.Contains(string(body), "VibeDE") {
		t.Fatalf("index = %d %.200s", resp.StatusCode, body)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("CSP = %q", resp.Header.Get("Content-Security-Policy"))
	}
	resp, body = get(t, ts, "/app.wasm")
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/wasm" || len(body) < 1000 {
		t.Fatalf("wasm = %d %s %d bytes", resp.StatusCode, resp.Header.Get("Content-Type"), len(body))
	}
	resp, _ = get(t, ts, "/style.css")
	if resp.StatusCode != 200 || !strings.Contains(resp.Header.Get("Content-Type"), "text/css") {
		t.Fatalf("css status = %d type=%s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
}

func TestSSEStreamsAgentEdit(t *testing.T) {
	_, ts, root := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("events status = %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	done := make(chan watch.Change, 1)
	go func() {
		event := ""
		for scanner.Scan() {
			line := scanner.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				event = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: ") && event == "change":
				var ch watch.Change
				if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ch) == nil {
					done <- ch
					return
				}
			}
		}
	}()
	time.Sleep(80 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case ch := <-done:
		if ch.Path != "main.go" {
			t.Fatalf("change = %+v", ch)
		}
		if ch.Kind != "modified" && ch.Kind != "created" {
			t.Fatalf("kind = %s", ch.Kind)
		}
		for _, h := range ch.Hunks {
			for _, ln := range h.Lines {
				if ln.Kind != " " && ln.Kind != "+" && ln.Kind != "-" {
					t.Fatalf("diff kind was not serialized as a unified-diff string: %q", ln.Kind)
				}
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for SSE change")
	}
}

func TestUnknownAPIPath(t *testing.T) {
	_, ts, _ := newTestServer(t)
	resp, _ := get(t, ts, "/api/nope")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestDetachEndpointUsesWindowOpener(t *testing.T) {
	s, ts, _ := newTestServer(t)

	req, _ := http.NewRequest("POST", ts.URL+"/api/detach", strings.NewReader(`{"path":"main.go","line":3}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("without opener status = %d, want 501", resp.StatusCode)
	}

	var gotURL, gotTitle string
	s.SetWindowOpener(func(u, title string) error {
		gotURL, gotTitle = u, title
		return nil
	})
	req, _ = http.NewRequest("POST", ts.URL+"/api/detach", strings.NewReader(`{"path":"main.go","line":3}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("detach status = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(gotURL, "detached=1") || !strings.Contains(gotURL, "path=main.go") || !strings.Contains(gotURL, "line=3") {
		t.Fatalf("detach URL = %q", gotURL)
	}
	if gotTitle != "VibeDE — main.go" {
		t.Fatalf("title = %q", gotTitle)
	}
	resp, body = get(t, ts, "/api/info")
	if !strings.Contains(string(body), `"detachable":true`) {
		t.Fatalf("info does not advertise detach: %s", body)
	}
}

func TestCodeSearchCaseSensitivity(t *testing.T) {
	_, ts, root := newTestServer(t)
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "ignored.js"), []byte("hello ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "case.txt"), []byte("VibeCase one\nnothing\nvibecase again and vibecase\nVIBECASE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin.dat"), []byte("hello\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	var insensitive codeSearchResponse
	resp, body := get(t, ts, "/api/search/code?q=vibecase&case=0")
	if resp.StatusCode != 200 {
		t.Fatalf("search status = %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &insensitive); err != nil {
		t.Fatal(err)
	}
	if len(insensitive.Matches) != 3 || insensitive.Files != 1 || insensitive.CaseSensitive {
		t.Fatalf("insensitive result = %+v", insensitive)
	}
	for _, m := range insensitive.Matches {
		if m.Path != "case.txt" {
			t.Fatalf("ignored or binary file leaked: %+v", m)
		}
	}
	if insensitive.Matches[1].Line != 3 || insensitive.Matches[1].Occurrences != 2 {
		t.Fatalf("line/occurrence mismatch: %+v", insensitive.Matches[1])
	}
	if !strings.Contains(insensitive.Matches[1].Preview, "vibecase again") || insensitive.Matches[1].MatchStart < 0 {
		t.Fatalf("preview = %+v", insensitive.Matches[1])
	}

	var sensitive codeSearchResponse
	resp, body = get(t, ts, "/api/search/code?q=vibecase&case=1")
	if resp.StatusCode != 200 {
		t.Fatalf("sensitive status = %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &sensitive); err != nil {
		t.Fatal(err)
	}
	if len(sensitive.Matches) != 1 || sensitive.Matches[0].Line != 3 || !sensitive.CaseSensitive {
		t.Fatalf("sensitive result = %+v", sensitive)
	}

	resp, body = get(t, ts, "/api/search/code?q=VibeCase&case=1")
	if resp.StatusCode != 200 {
		t.Fatalf("mixed-case status = %d %s", resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, &sensitive); err != nil {
		t.Fatal(err)
	}
	if len(sensitive.Matches) != 1 || sensitive.Matches[0].Line != 1 {
		t.Fatalf("mixed-case result = %+v", sensitive)
	}

	resp, body = get(t, ts, "/api/search/code?q=definitely-absent")
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"matches":[]`) {
		t.Fatalf("empty result = %d %s", resp.StatusCode, body)
	}
}
