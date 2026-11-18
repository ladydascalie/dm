//go:build !js

// Command dm-wasm runs the real dm client in a browser tab, as the spike for a
// guided tour. It only runs under js/wasm. This native stub exists so
// `go build ./...` and the layer gate see the package on every platform.
package main

import "fmt"

func main() {
	fmt.Println("dm-wasm runs in a browser: see cmd/dm-wasm/README.md")
}
