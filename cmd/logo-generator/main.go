// Command logo-generator renders per-site logos from configs/*.json:
// text and image slots merged into an SVG template, then rasterized to
// PNG/WebP/AVIF (and optionally JPEG) into output/<name>/.
package main

import (
	"fmt"
	"os"

	"example.com/logo-generator/internal/cli"
)

// version is the application SemVer, injected at build time via
// -ldflags "-X main.version=$(APP_VERSION)" (see Makefile). Dev builds
// report "dev".
var version = "dev"

func main() {
	if err := cli.Run(os.Args[1:], os.Stdout, version); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
