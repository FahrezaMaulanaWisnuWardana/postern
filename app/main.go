// Command yourpm is the native desktop client. It links only the Gio toolkit
// and pure-Go libraries, so the result is a single self-contained binary with
// no Electron, webview or embedded browser.
package main

import (
	"fmt"
	"os"

	"yourpm/cli"
	"yourpm/core/brand"
	"yourpm/ui"
)

func main() {
	if len(os.Args) > 1 {
		os.Exit(cli.Main(os.Args[1:]))
	}
	if err := ui.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", brand.Name, err)
		os.Exit(1)
	}
}
