//go:build js && wasm

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"syscall/js"
	"time"
)

type httpError struct {
	Status int
	API    APIError
}

func (e *httpError) Error() string {
	if e.API.Error != "" {
		return e.API.Error
	}
	return "HTTP " + strconv.Itoa(e.Status)
}

func request(method, path string, payload any) ([]byte, int, error) {
	opts := js.Global().Get("Object").New()
	opts.Set("method", method)
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		headers := js.Global().Get("Object").New()
		headers.Set("Content-Type", "application/json")
		opts.Set("headers", headers)
		opts.Set("body", string(body))
	}
	ch := make(chan struct {
		data   []byte
		status int
		err    error
	}, 1)
	then := js.FuncOf(func(_ js.Value, args []js.Value) any {
		resp := args[0]
		status := resp.Get("status").Int()
		textThen := js.FuncOf(func(_ js.Value, args2 []js.Value) any {
			data := ""
			if len(args2) > 0 {
				data = args2[0].String()
			}
			ch <- struct {
				data   []byte
				status int
				err    error
			}{data: []byte(data), status: status}
			return nil
		})
		resp.Call("text").Call("then", textThen)
		return nil
	})
	catch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		msg := "network error"
		if len(args) > 0 {
			msg = args[0].Get("message").String()
		}
		ch <- struct {
			data   []byte
			status int
			err    error
		}{err: errors.New(msg)}
		return nil
	})
	retained = append(retained, then, catch)
	js.Global().Call("fetch", path, opts).Call("then", then).Call("catch", catch)
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, 0, r.err
		}
		if r.status >= 400 {
			var api APIError
			_ = json.Unmarshal(r.data, &api)
			return r.data, r.status, &httpError{Status: r.status, API: api}
		}
		return r.data, r.status, nil
	case <-time.After(90 * time.Second):
		return nil, 0, errors.New("request timed out")
	}
}

func apiGet(path string, out any) error {
	data, _, err := request("GET", path, nil)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func apiPost(path string, body any, out any) error {
	data, _, err := request("POST", path, body)
	if err != nil {
		return err
	}
	if out == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func withParams(base string, params map[string]string) string {
	v := url.Values{}
	for k, val := range params {
		v.Set(k, val)
	}
	if len(v) == 0 {
		return base
	}
	return base + "?" + v.Encode()
}

func fetchInfo() (Info, error) {
	var info Info
	err := apiGet("/api/info", &info)
	return info, err
}

func fetchTree(path string) ([]Entry, error) {
	var resp TreeResponse
	err := apiGet(withParams("/api/tree", map[string]string{"path": path}), &resp)
	return resp.Entries, err
}

func fetchFile(path string) (FileResponse, error) {
	var f FileResponse
	err := apiGet(withParams("/api/file", map[string]string{"path": path}), &f)
	return f, err
}

func fetchAnalysis(path string) (Analysis, error) {
	var a Analysis
	err := apiGet(withParams("/api/analysis", map[string]string{"path": path}), &a)
	return a, err
}

func fetchDiagnostics(path string) (FileDiagnostics, error) {
	var d FileDiagnostics
	err := apiGet(withParams("/api/diagnostics", map[string]string{"path": path}), &d)
	return d, err
}

func fetchAllDiagnostics() ([]FileDiagnostics, error) {
	var resp struct {
		Files []FileDiagnostics `json:"files"`
	}
	err := apiGet("/api/diagnostics", &resp)
	return resp.Files, err
}

func fetchServers() ([]Server, error) {
	var resp struct {
		Servers []Server `json:"servers"`
	}
	err := apiGet("/api/servers", &resp)
	return resp.Servers, err
}

func openDocument(path string) error {
	return apiPost("/api/lsp/open", map[string]string{"path": path}, nil)
}

func navigate(path string, line, character int, method string) ([]NavLocation, error) {
	var resp struct {
		Locations []NavLocation `json:"locations"`
	}
	err := apiPost("/api/navigate", map[string]any{
		"path": path, "line": line, "character": character, "method": method,
	}, &resp)
	return resp.Locations, err
}

func fetchDiagram(path, kind, function string) (DiagramDoc, error) {
	var d DiagramDoc
	params := map[string]string{"path": path, "kind": kind}
	if function != "" {
		params["function"] = function
	}
	err := apiGet(withParams("/api/diagram", params), &d)
	return d, err
}

func searchCode(q string, caseSensitive bool) (CodeSearchResponse, error) {
	var resp CodeSearchResponse
	caseParam := "0"
	if caseSensitive {
		caseParam = "1"
	}
	err := apiGet(withParams("/api/search/code", map[string]string{"q": q, "case": caseParam}), &resp)
	return resp, err
}

func searchFiles(q string) ([]Entry, error) {
	var resp struct {
		Results []Entry `json:"results"`
	}
	err := apiGet(withParams("/api/search", map[string]string{"q": q}), &resp)
	return resp.Results, err
}

func restartServer(key string) ([]Server, error) {
	var resp struct {
		Servers []Server `json:"servers"`
	}
	err := apiPost("/api/servers/restart", map[string]string{"key": key}, &resp)
	return resp.Servers, err
}

func rescanServers() ([]Server, error) {
	var resp struct {
		Servers []Server `json:"servers"`
	}
	err := apiPost("/api/servers/rescan", map[string]any{}, &resp)
	return resp.Servers, err
}

func apiErrorHint(err error) string {
	var he *httpError
	if errors.As(err, &he) {
		if he.API.Install != "" {
			return he.API.Install
		}
		if he.API.Error != "" {
			return he.API.Error
		}
	}
	return fmt.Sprint(err)
}

func apiErrorInstall(err error) string {
	var he *httpError
	if errors.As(err, &he) {
		return he.API.Install
	}
	return ""
}
