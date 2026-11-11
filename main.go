// Command logo-generator renders per-site SVG logos from JSON configs and
// exports them via resvg, cwebp and avifenc.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// version is the application SemVer, injected at build time via
// -ldflags "-X main.version=$(APP_VERSION)" (see Makefile). Dev builds
// report "dev".
var version = "dev"

func main() {
	configsPath := flag.String("configs", "configs", "config file or directory of *.json site configs")
	outDir := flag.String("out", "output", "output root directory")
	sitesFlag := flag.String("sites", "", "comma-separated site names to render (default: all)")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *versionFlag {
		fmt.Println("logo-generator", version)
		return
	}

	if err := runAll(*configsPath, *outDir, *sitesFlag); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func runAll(configsPath, outDir, sitesFlag string) error {
	files, err := configFiles(configsPath)
	if err != nil {
		return err
	}

	var filter map[string]bool
	if sitesFlag != "" {
		filter = map[string]bool{}
		for _, s := range strings.Split(sitesFlag, ",") {
			if s = strings.TrimSpace(s); s != "" {
				filter[s] = true
			}
		}
	}

	var failures []string
	seen := map[string]string{}
	rendered := 0
	matched := 0

	for _, f := range files {
		cfg, err := loadConfig(f)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		if prev, dup := seen[cfg.Name]; dup {
			failures = append(failures, fmt.Sprintf("%s: duplicate site name %q (also in %s)", f, cfg.Name, prev))
			continue
		}
		seen[cfg.Name] = f
		if filter != nil && !filter[cfg.Name] {
			continue
		}
		matched++

		siteDir := filepath.Join(outDir, cfg.Name)
		fmt.Printf("== %s (%s)\n", cfg.Name, f)
		if err := renderSite(cfg, siteDir); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", f, err))
			continue
		}
		rendered++
	}

	if filter != nil && matched == 0 && len(failures) == 0 {
		return fmt.Errorf("no sites matched -sites %q", sitesFlag)
	}
	if len(failures) > 0 {
		return fmt.Errorf("%d site(s) failed:\n - %s", len(failures), strings.Join(failures, "\n - "))
	}
	if rendered == 0 {
		return errors.New("nothing to render")
	}
	fmt.Printf("done: %d site(s) -> %s/<name>/logo.*\n", rendered, outDir)
	return nil
}
