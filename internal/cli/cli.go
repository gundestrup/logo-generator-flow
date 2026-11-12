// Package cli runs the logo-generator command line: flag parsing,
// config discovery, per-site rendering, and the browser editor.
package cli

import (
	"errors"

	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"example.com/logo-generator/internal/config"
	"example.com/logo-generator/internal/render"
	"example.com/logo-generator/internal/server"
)

// Run executes the CLI and returns a non-nil error on any failure.
func Run(argv []string, out io.Writer, version string) error {
	fs := flag.NewFlagSet("logo-generator", flag.ContinueOnError)
	fs.SetOutput(io.Discard) // caller reports the parse error
	configsPath := fs.String("configs", "configs", "config file or directory of *.json site configs")
	outDir := fs.String("out", "output", "output root directory")
	sitesFlag := fs.String("sites", "", "comma-separated site names to render (default: all)")
	versionFlag := fs.Bool("version", false, "print version and exit")
	editFlag := fs.Bool("edit", false, "start the browser editor (alias for -ui)")
	uiFlag := fs.Bool("ui", false, "start the browser UI on http://127.0.0.1:8181/")
	if err := fs.Parse(argv); err != nil {
		return err
	}

	if *versionFlag {
		_, _ = fmt.Fprintln(out, "logo-generator", version)
		return nil
	}
	if *editFlag || *uiFlag {
		return server.Serve(*configsPath, *outDir, "127.0.0.1:8181", version)
	}
	return runAll(*configsPath, *outDir, *sitesFlag)
}

func runAll(configsPath, outDir, sitesFlag string) error {
	files, err := config.Files(configsPath)
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
	cfg, err := config.Load(file)
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
	if err := render.Site(cfg, siteDir); err != nil {
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
