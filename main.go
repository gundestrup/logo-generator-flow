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

	filter := siteFilter(sitesFlag)
	res := runResult{seen: map[string]string{}}
	for _, f := range files {
		res.render(f, outDir, filter)
	}
	return res.finish(sitesFlag, filter, outDir)
}

// runResult accumulates per-site outcomes across a multi-config run.
type runResult struct {
	failures []string
	seen     map[string]string
	rendered int
	matched  int
}

func siteFilter(sitesFlag string) map[string]bool {
	if sitesFlag == "" {
		return nil
	}
	filter := map[string]bool{}
	for _, s := range strings.Split(sitesFlag, ",") {
		if s = strings.TrimSpace(s); s != "" {
			filter[s] = true
		}
	}
	return filter
}

func (r *runResult) render(file, outDir string, filter map[string]bool) {
	cfg, err := loadConfig(file)
	if err != nil {
		r.failures = append(r.failures, fmt.Sprintf("%s: %v", file, err))
		return
	}
	if prev, dup := r.seen[cfg.Name]; dup {
		r.failures = append(r.failures, fmt.Sprintf("%s: duplicate site name %q (also in %s)", file, cfg.Name, prev))
		return
	}
	r.seen[cfg.Name] = file
	if filter != nil && !filter[cfg.Name] {
		return
	}
	r.matched++

	siteDir := filepath.Join(outDir, cfg.Name)
	fmt.Printf("== %s (%s)\n", cfg.Name, file)
	if err := renderSite(cfg, siteDir); err != nil {
		r.failures = append(r.failures, fmt.Sprintf("%s: %v", file, err))
		return
	}
	r.rendered++
}

func (r *runResult) finish(sitesFlag string, filter map[string]bool, outDir string) error {
	if filter != nil && r.matched == 0 && len(r.failures) == 0 {
		return fmt.Errorf("no sites matched -sites %q", sitesFlag)
	}
	if len(r.failures) > 0 {
		return fmt.Errorf("%d site(s) failed:\n - %s", len(r.failures), strings.Join(r.failures, "\n - "))
	}
	if r.rendered == 0 {
		return errors.New("nothing to render")
	}
	fmt.Printf("done: %d site(s) -> %s/<name>/logo.*\n", r.rendered, outDir)
	return nil
}
