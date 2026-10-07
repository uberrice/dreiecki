package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// RenderOptions are passed through to kicad-cli.
type RenderOptions struct {
	KicadCLI         string
	Theme            string
	ExcludeDrawSheet bool
}

// RenderedPage is the SVG of one sheet instance.
type RenderedPage struct {
	SVG           []byte
	Width, Height float64 // mm, from the SVG viewBox
}

var plottedRe = regexp.MustCompile(`Plotted to '([^']+)'`)
var viewBoxRe = regexp.MustCompile(`viewBox="([-\d.]+) ([-\d.]+) ([\d.]+) ([\d.]+)"`)

// Render plots every page of the project with kicad-cli and returns the SVGs
// keyed by sheet instance path.
func Render(p *Project, workDir string, opt RenderOptions) (map[string]*RenderedPage, error) {
	rootFile, err := p.Materialize(filepath.Join(workDir, "src"))
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(workDir, "svg")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	args := []string{"sch", "export", "svg", "--no-background-color", "-o", outDir}
	if opt.Theme != "" {
		args = append(args, "--theme", opt.Theme)
	}
	if opt.ExcludeDrawSheet {
		args = append(args, "--exclude-drawing-sheet")
	}
	args = append(args, rootFile)
	cmd := exec.Command(opt.KicadCLI, args...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("kicad-cli failed: %v\n%s", err, out.String())
	}

	// kicad-cli names files <root>-<sheet>-<subsheet>.svg; match those first and
	// fall back to plot order for anything it sanitized differently.
	var plotted []string
	for _, m := range plottedRe.FindAllStringSubmatch(out.String(), -1) {
		plotted = append(plotted, filepath.Base(m[1]))
	}
	used := map[string]bool{}
	assign := map[*SheetInst]string{}
	for _, s := range p.Sheets {
		name := strings.Join(append([]string{p.Name}, s.Names...), "-") + ".svg"
		if _, err := os.Stat(filepath.Join(outDir, name)); err == nil && !used[name] {
			assign[s], used[name] = name, true
		}
	}
	var spare []string
	for _, f := range plotted {
		if !used[f] {
			spare = append(spare, f)
		}
	}
	for _, s := range p.Sheets {
		if _, ok := assign[s]; !ok && len(spare) > 0 {
			assign[s], spare = spare[0], spare[1:]
		}
	}

	pages := map[string]*RenderedPage{}
	for s, name := range assign {
		data, err := os.ReadFile(filepath.Join(outDir, name))
		if err != nil {
			return nil, err
		}
		rp := &RenderedPage{SVG: data, Width: 297, Height: 210}
		if m := viewBoxRe.FindSubmatch(data[:min(len(data), 4096)]); m != nil {
			rp.Width, rp.Height = num(string(m[3])), num(string(m[4]))
		}
		pages[s.Key()] = rp
	}
	return pages, nil
}
