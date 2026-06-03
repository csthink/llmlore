// Command llmlore is the entry point for the llmlore CLI.
//
// It stays intentionally thin: all command wiring lives in internal/cli so the
// binary can be reused (and tested) without depending on package main.
package main

import "github.com/csthink/llmlore/internal/cli"

func main() {
	cli.Execute()
}
