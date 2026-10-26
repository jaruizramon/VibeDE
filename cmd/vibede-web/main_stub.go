//go:build !js

// The real UI is compiled to WebAssembly by `make web`.
package main

import "fmt"

func main() {
	fmt.Println("VibeDE's browser UI is built for GOOS=js GOARCH=wasm; run `make web`.")
}
