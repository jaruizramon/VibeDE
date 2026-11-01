package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"vibede/internal/config"
)

// fakeLLM serves the two endpoints explain.go uses and records the last chat
// request so tests can inspect the prompt.
type fakeLLM struct {
	*httptest.Server
	mu       sync.Mutex
	last     chatRequest
	failWith int
	reply    string
}

func newFakeLLM(t *testing.T) *fakeLLM {
	t.Helper()
	f := &fakeLLM{
		reply: "- **Line 3**: starts the walk.\n- **Line 4-6**: visits each child once.",
	}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "unloaded-preset", "status": map[string]string{"value": "unloaded"}},
					{"id": "tiny-test", "status": map[string]string{"value": "loaded"}},
				},
			})
		case "/v1/chat/completions":
			var req chatRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.mu.Lock()
			f.last = req
			failWith := f.failWith
			reply := f.reply
			f.mu.Unlock()
			if failWith != 0 {
				w.WriteHeader(failWith)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"error": map[string]string{"message": "boom"},
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{{
					"message": map[string]string{"content": reply},
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func newExplainServer(t *testing.T, ai config.AIConfig) (*httptest.Server, string) {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"main.js": `import { helper } from "./util.js";
import { gone } from "../../outside.js";

export function walk(node) {
  for (const child of node.children) {
    helper(child);
  }
}
`,
		"util.js": `export function helper(x) {
  return x * 2;
}
`,
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.NoServers = true
	cfg.AI = ai
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(func() {
		ts.Close()
		s.Close()
	})
	return ts, root
}

func postExplain(t *testing.T, ts *httptest.Server, req explainRequest) (*http.Response, []byte) {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/api/explain", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatal(err)
	}
	return resp, buf.Bytes()
}

func TestParseAnnotations(t *testing.T) {
	answer := "- **Line 125**: Defines the function.\n" +
		"- **Line 126-128**: Guards against a second match,\n" +
		"  wrapping onto another line.\n" +
		"\n" +
		"Summary outside the list.\n" +
		"- Line 130: sets netMode.\n" +
		"- **Lines 131 – 132**: marks the match local.\n"
	got := parseAnnotations(answer)
	if len(got) != 4 {
		t.Fatalf("annotations = %+v", got)
	}
	if got[0].StartLine != 125 || got[0].EndLine != 125 || got[0].Text != "Defines the function." {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].StartLine != 126 || got[1].EndLine != 128 ||
		!strings.Contains(got[1].Text, "wrapping onto another line.") {
		t.Fatalf("range continuation = %+v", got[1])
	}
	if got[2].StartLine != 130 || strings.Contains(got[2].Text, "Summary") {
		t.Fatalf("prose leaked into note = %+v", got[2])
	}
	if got[3].StartLine != 131 || got[3].EndLine != 132 {
		t.Fatalf("dash range = %+v", got[3])
	}
	if got := parseAnnotations("A prose answer without line anchors."); len(got) != 0 {
		t.Fatalf("prose parsed as annotations: %+v", got)
	}
}

func TestExplainSendsSelectionFileAndLinkedImports(t *testing.T) {
	llm := newFakeLLM(t)
	ts, _ := newExplainServer(t, config.AIConfig{
		Enabled:   true,
		Endpoint:  llm.URL + "/v1",
		MaxTokens: 128,
	})
	resp, body := postExplain(t, ts, explainRequest{
		Path:      "main.js",
		Language:  "javascript",
		Function:  "walk",
		StartLine: 3,
		EndLine:   6,
		Code:      "  for (const child of node.children) {\n    helper(child);\n  }",
		Question:  "What does this loop do?",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var got explainResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Explanation, "- **Line 3**") || got.Model != "tiny-test" {
		t.Fatalf("response = %+v", got)
	}
	if strings.Join(got.Context, ",") != "main.js,util.js" {
		t.Fatalf("context = %v, want only workspace files linked by import", got.Context)
	}
	if len(got.Annotations) != 2 {
		t.Fatalf("annotations = %+v", got.Annotations)
	}
	if got.Annotations[0].StartLine != 3 || got.Annotations[0].EndLine != 3 ||
		got.Annotations[0].Text != "starts the walk." {
		t.Fatalf("first annotation = %+v", got.Annotations[0])
	}
	if got.Annotations[1].StartLine != 4 || got.Annotations[1].EndLine != 6 {
		t.Fatalf("range annotation = %+v", got.Annotations[1])
	}

	// The parsed notes are persisted with the answer in the .rab history.
	_, archBody := get(t, ts, "/api/archive")
	var arch archiveResponse
	if err := json.Unmarshal(archBody, &arch); err != nil {
		t.Fatal(err)
	}
	if len(arch.Explanations) != 1 || len(arch.Explanations[0].Annotations) != 2 {
		t.Fatalf("archived explanation = %+v", arch.Explanations)
	}
	if arch.Explanations[0].Annotations[1].Text != "visits each child once." {
		t.Fatalf("archived notes = %+v", arch.Explanations[0].Annotations)
	}

	llm.mu.Lock()
	last := llm.last
	llm.mu.Unlock()
	if last.Model != "tiny-test" || last.MaxTokens != 128 || last.Stream {
		t.Fatalf("chat request = %+v", last)
	}
	if got := last.ChatTemplateKwargs["enable_thinking"]; got != false {
		t.Fatalf("enable_thinking = %v, want false", got)
	}
	var prompt strings.Builder
	for _, m := range last.Messages {
		prompt.WriteString(m.Content)
		prompt.WriteString("\n")
	}
	joined := prompt.String()
	for _, want := range []string{
		"What does this loop do?",
		"helper(child)",
		"=== file: main.js",
		"=== file: util.js",
		"return x * 2;",
		"- **Line 125**: what this line does",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("prompt missing %q:\n%s", want, joined)
		}
	}
	// The import line itself mentions outside.js, but that file must not be
	// included as a context document.
	if strings.Contains(joined, "=== file: ../") {
		t.Fatalf("prompt included a path outside the workspace:\n%s", joined)
	}
}

func TestExplainContextStaysUnderBudget(t *testing.T) {
	llm := newFakeLLM(t)
	ts, _ := newExplainServer(t, config.AIConfig{Enabled: true, Endpoint: llm.URL + "/v1"})
	resp, body := postExplain(t, ts, explainRequest{
		Path:     "main.js",
		Code:     strings.Repeat("a", 60<<10),
		Question: "explain",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	llm.mu.Lock()
	user := llm.last.Messages[1].Content
	llm.mu.Unlock()
	if len(user) > maxExplainContext+512 {
		t.Fatalf("prompt context = %d bytes, want <= %d", len(user), maxExplainContext+512)
	}
}

func TestExplainDisabled(t *testing.T) {
	ts, _ := newExplainServer(t, config.AIConfig{Enabled: false, Endpoint: "http://127.0.0.1:1/v1"})
	resp, body := postExplain(t, ts, explainRequest{Path: "main.js", Code: "x", Question: "q"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	var api apiError
	_ = json.Unmarshal(body, &api)
	if api.Code != "ai_unconfigured" {
		t.Fatalf("body = %s", body)
	}
}

func TestExplainUpstreamError(t *testing.T) {
	llm := newFakeLLM(t)
	llm.failWith = http.StatusInternalServerError
	ts, _ := newExplainServer(t, config.AIConfig{Enabled: true, Endpoint: llm.URL + "/v1"})
	resp, body := postExplain(t, ts, explainRequest{Path: "main.js", Code: "x", Question: "q"})
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "boom") {
		t.Fatalf("body = %s", body)
	}
}

func TestExplainRejectsEmptySelection(t *testing.T) {
	ts, _ := newExplainServer(t, config.AIConfig{Enabled: true, Endpoint: "http://127.0.0.1:1/v1"})
	resp, body := postExplain(t, ts, explainRequest{Path: "main.js", Code: "   ", Question: "q"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}
}
