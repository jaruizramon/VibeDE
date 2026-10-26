package lsp

import (
	"encoding/json"
	"testing"
)

func TestServerApplyEditIsRejected(t *testing.T) {
	res, rpcErr := serverRequestResult("workspace/applyEdit", json.RawMessage(`{
		"label": "format",
		"edit": {"changes": {"/tmp/x.go": [{"newText": "evil"}]}}
	}`))
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	m, ok := res.(map[string]any)
	if !ok || m["applied"] != false {
		t.Fatalf("applyEdit result = %#v", res)
	}
	if m["failureReason"] == "" {
		t.Fatal("expected a failure reason")
	}
}

func TestServerConfigurationDefaults(t *testing.T) {
	params := json.RawMessage(`{"items":[{"section":"css"},{"section":"json"},{"section":"unknown"}]}`)
	res, rpcErr := serverRequestResult("workspace/configuration", params)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	vals, ok := res.([]any)
	if !ok || len(vals) != 3 {
		t.Fatalf("configuration result = %#v", res)
	}
	css := vals[0].(map[string]any)
	if css["validate"] != true {
		t.Fatalf("css config = %#v", css)
	}
	jsonCfg := vals[1].(map[string]any)
	validate, ok := jsonCfg["validate"].(map[string]any)
	if !ok || validate["enable"] != true {
		t.Fatalf("json config = %#v", jsonCfg)
	}
	if len(vals[2].(map[string]any)) != 0 {
		t.Fatalf("unknown section = %#v", vals[2])
	}
}

func TestUnsupportedServerRequestReturnsError(t *testing.T) {
	_, rpcErr := serverRequestResult("workspace/executeCommand", nil)
	if rpcErr == nil || rpcErr.Code != -32601 {
		t.Fatalf("error = %#v", rpcErr)
	}
}
