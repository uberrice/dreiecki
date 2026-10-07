package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
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
		rp, err := readSVG(filepath.Join(outDir, name))
		if err != nil {
			return nil, err
		}
		pages[s.Key()] = rp
	}
	return pages, nil
}

func readSVG(file string) (*RenderedPage, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	rp := &RenderedPage{SVG: data, Width: 297, Height: 210}
	if m := viewBoxRe.FindSubmatch(data[:min(len(data), 4096)]); m != nil {
		rp.Width, rp.Height = num(string(m[3])), num(string(m[4]))
	}
	return rp, nil
}

// cliSlots limits concurrent kicad-cli runs; PCB reports start one per layer and revision.
var cliSlots = make(chan struct{}, max(2, min(runtime.NumCPU(), 8)))

// RenderPCB plots each page's layers into its own SVG, keyed by page ID.
// Pages whose layer the board doesn't have are left out.
func RenderPCB(b *Board, pages []pcbPage, workDir string, opt RenderOptions) (map[string]*RenderedPage, error) {
	file, err := b.Materialize(filepath.Join(workDir, "src"))
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(workDir, "svg")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		rendered = map[string]*RenderedPage{}
		first    error
	)
	for i, pg := range pages {
		if pg.Layer != "" && !b.has(pg.Layer) {
			continue
		}
		var layers []string
		for _, l := range pg.Layers {
			if b.has(l) {
				layers = append(layers, l)
			}
		}
		out := filepath.Join(outDir, fmt.Sprintf("%02d.svg", i))
		args := []string{"pcb", "export", "svg", "-l", strings.Join(layers, ","), "-o", out}
		if opt.Theme != "" {
			args = append(args, "--theme", opt.Theme)
		}
		if opt.ExcludeDrawSheet {
			args = append(args, "--exclude-drawing-sheet")
		}
		args = append(args, file)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			cliSlots <- struct{}{}
			defer func() { <-cliSlots }()
			run := func(args []string) (string, error) {
				cmd := exec.Command(opt.KicadCLI, args...)
				var log bytes.Buffer
				cmd.Stdout, cmd.Stderr = &log, &log
				err := cmd.Run()
				if err == nil {
					_, err = os.Stat(out)
				}
				return log.String(), err
			}
			log, err := run(args)
			if err != nil {
				// KiCad 9 added --mode-single/--mode-multi; ask for a single file explicitly.
				if _, err2 := run(append([]string{"pcb", "export", "svg", "--mode-single"}, args[3:]...)); err2 == nil {
					err = nil
				}
			}
			var rp *RenderedPage
			if err == nil {
				rp, err = readSVG(out)
			}
			mu.Lock()
			defer mu.Unlock()
			if err != nil && first == nil {
				first = fmt.Errorf("kicad-cli failed: %v\n%s", err, log)
			}
			rendered[id] = rp
		}(pg.ID)
	}
	wg.Wait()
	return rendered, first
}
