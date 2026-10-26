//go:build js && wasm

package main

import (
	"encoding/json"
	"syscall/js"
)

func connectEvents(a *app) {
	es := js.Global().Get("EventSource").New("/api/events")
	a.es = es
	add := func(name string, fn func([]byte)) {
		handler := js.FuncOf(func(_ js.Value, args []js.Value) any {
			if len(args) == 0 {
				return nil
			}
			data := args[0].Get("data").String()
			go fn([]byte(data))
			return nil
		})
		retained = append(retained, handler)
		es.Call("addEventListener", name, handler)
	}
	add("ready", func(data []byte) {
		var ready struct {
			Root    string `json:"root"`
			Version string `json:"version"`
		}
		_ = json.Unmarshal(data, &ready)
		a.setConnected(true)
		go a.applyAllDiagnostics()
	})
	add("change", func(data []byte) {
		var ch Change
		if json.Unmarshal(data, &ch) != nil {
			return
		}
		a.onChange(ch)
	})
	add("diagnostics", func(data []byte) {
		var fd FileDiagnostics
		if json.Unmarshal(data, &fd) != nil {
			return
		}
		a.onDiagnostics(fd)
	})
	add("server", func(data []byte) {
		var srv Server
		if json.Unmarshal(data, &srv) != nil {
			return
		}
		a.onServer(srv)
	})
	open := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		a.setConnected(true)
		return nil
	})
	onErr := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		a.setConnected(false)
		return nil
	})
	retained = append(retained, open, onErr)
	es.Call("addEventListener", "open", open)
	es.Call("addEventListener", "error", onErr)
}
