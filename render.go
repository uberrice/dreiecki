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

// RenderOptions are passed through to kicad-cli. Without KicadCLI the built-in renderer is used.
type RenderOptions struct {
	KicadCLI         string
	Builtin          bool // use the built-in renderer even if kicad-cli is installed
	Theme            string
	ExcludeDrawSheet bool
}

// RenderedPage is the SVG of one sheet instance or board page.
type RenderedPage struct {
	SVG           []byte
	Width, Height float64     // mm, from the SVG viewBox
	Layers        []*LayerSVG // stacked board pages: one SVG per layer, SVG is unused
}

// LayerSVG is one layer of a stacked board page.
type LayerSVG struct {
	Layer string
	SVG   []byte
}

var viewBoxRe = regexp.MustCompile(`viewBox="([-\d.]+) ([-\d.]+) ([\d.]+) ([\d.]+)"`)

// Render plots every page of the project with kicad-cli and returns the SVGs
// keyed by sheet instance path.
func Render(p *Project, workDir string, opt RenderOptions) (map[string]*RenderedPage, error) {
	if opt.KicadCLI == "" {
		pages := map[string]*RenderedPage{}
		for _, s := range p.Sheets {
			pages[s.Key()] = renderSheetBuiltin(p, s, !opt.ExcludeDrawSheet)
		}
		return pages, nil
	}
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

	assign, err := matchSheetSVGs(p, outDir)
	if err != nil {
		return nil, err
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
// Stacked pages get one SVG per layer so the viewer can choose their order and opacity.
// Pages whose layer the board doesn't have are left out.
func RenderPCB(b *Board, pages []pcbPage, workDir string, opt RenderOptions) (map[string]*RenderedPage, error) {
	if opt.KicadCLI == "" {
		return renderPCBBuiltin(b, pages, opt), nil
	}
	file, err := b.Materialize(filepath.Join(workDir, "src"))
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(workDir, "svg")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	// Multi-file plotting numbers drawing-sheet frames differently. Batch only
	// frame-free reports so layer additions cannot create differences in the frame.
	if opt.ExcludeDrawSheet {
		if rendered, ok := renderPCBBatch(b, pages, file, outDir, opt); ok {
			return rendered, nil
		}
	}
	var (
		mu       sync.Mutex
		wg       sync.WaitGroup
		rendered = map[string]*RenderedPage{}
		first    error
	)
	plot := func(layers []string, out string, done func(*RenderedPage)) {
		args := []string{"pcb", "export", "svg", "-l", strings.Join(layers, ","), "-o", out}
		if opt.Theme != "" {
			args = append(args, "--theme", opt.Theme)
		}
		if opt.ExcludeDrawSheet {
			args = append(args, "--exclude-drawing-sheet")
		}
		args = append(args, file)
		wg.Add(1)
		go func() {
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
			done(rp)
		}()
	}
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
		if !pg.Stack {
			id := pg.ID
			plot(layers, filepath.Join(outDir, fmt.Sprintf("%02d.svg", i)), func(rp *RenderedPage) { rendered[id] = rp })
			continue
		}
		stack := &RenderedPage{Layers: make([]*LayerSVG, len(layers))}
		mu.Lock()
		rendered[pg.ID] = stack
		mu.Unlock()
		for j, l := range layers {
			plot([]string{l}, filepath.Join(outDir, fmt.Sprintf("%02d-%02d.svg", i, j)), func(rp *RenderedPage) {
				if rp != nil {
					stack.Layers[j] = &LayerSVG{Layer: l, SVG: rp.SVG}
					stack.Width, stack.Height = rp.Width, rp.Height
				}
			})
		}
	}
	wg.Wait()
	return rendered, first
}

// renderPCBBatch uses one export for overview layers and one for detail layers.
// Unknown CLI versions, unsupported page shapes or filenames use single plots.
func renderPCBBatch(b *Board, pages []pcbPage, file, outDir string, opt RenderOptions) (map[string]*RenderedPage, bool) {
	help, err := exec.Command(opt.KicadCLI, "pcb", "export", "svg", "--help").Output()
	if err != nil || !bytes.Contains(help, []byte("--mode-multi")) || !bytes.Contains(help, []byte("--common-layers")) {
		return nil, false
	}
	var stackLayers, detailLayers []string
	for _, pg := range pages {
		if pg.Layer != "" && !b.has(pg.Layer) {
			continue
		}
		if pg.Stack {
			for _, l := range pg.Layers {
				if b.has(l) {
					stackLayers = union(stackLayers, []string{l})
				}
			}
		} else {
			// The detail page consists of its own layer and the board outline.
			if pg.Layer == "" || len(pg.Layers) != 2 || pg.Layers[0] != pg.Layer || pg.Layers[1] != "Edge.Cuts" {
				return nil, false
			}
			detailLayers = union(detailLayers, []string{pg.Layer})
		}
	}
	export := func(layers []string, common, group string) (map[string]*RenderedPage, bool) {
		rendered := map[string]*RenderedPage{}
		if len(layers) == 0 {
			return rendered, true
		}
		dir := filepath.Join(outDir, group)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, false
		}
		names := map[string]bool{}
		for _, l := range layers {
			name := b.Name + "-" + strings.ReplaceAll(b.layerName(l), ".", "_") + ".svg"
			if names[name] {
				return nil, false
			}
			names[name] = true
		}
		args := []string{"pcb", "export", "svg", "--mode-multi", "--exclude-drawing-sheet", "-l", strings.Join(layers, ","), "-o", dir}
		if common != "" && b.has(common) {
			args = append(args, "--common-layers", common)
		}
		if opt.Theme != "" {
			args = append(args, "--theme", opt.Theme)
		}
		args = append(args, file)
		cliSlots <- struct{}{}
		_, err := exec.Command(opt.KicadCLI, args...).CombinedOutput()
		<-cliSlots
		if err != nil {
			return nil, false
		}
		for _, l := range layers {
			name := b.Name + "-" + strings.ReplaceAll(b.layerName(l), ".", "_") + ".svg"
			rp, err := readSVG(filepath.Join(dir, name))
			if err != nil {
				return nil, false
			}
			rendered[l] = rp
		}
		return rendered, true
	}
	overview, ok := export(stackLayers, "", "overview")
	if !ok {
		return nil, false
	}
	detail, ok := export(detailLayers, "Edge.Cuts", "detail")
	if !ok {
		return nil, false
	}
	rendered := map[string]*RenderedPage{}
	for _, pg := range pages {
		if pg.Layer != "" && !b.has(pg.Layer) {
			continue
		}
		if !pg.Stack {
			rendered[pg.ID] = detail[pg.Layer]
			continue
		}
		stack := &RenderedPage{}
		for _, l := range pg.Layers {
			if rp := overview[l]; rp != nil {
				stack.Layers = append(stack.Layers, &LayerSVG{Layer: l, SVG: rp.SVG})
				stack.Width, stack.Height = rp.Width, rp.Height
			}
		}
		rendered[pg.ID] = stack
	}
	return rendered, true
}

// Match KiCad's hierarchy filenames, including its separator sanitization.
// Never infer a sheet's identity from directory order or localized CLI output.
func matchSheetSVGs(p *Project, dir string) (map[*SheetInst]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	files := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".svg") {
			files[e.Name()] = true
		}
	}
	assigned := map[*SheetInst]string{}
	used := map[string]bool{}
	for _, sheet := range p.Sheets {
		name := strings.Join(append([]string{p.Name}, sheet.Names...), "-")
		name = strings.NewReplacer("/", "_", "\\", "_").Replace(name) + ".svg"
		if !files[name] {
			return nil, fmt.Errorf("no SVG matching sheet %s (expected %q)", sheet.DisplayName(), name)
		}
		if used[name] {
			return nil, fmt.Errorf("ambiguous SVG filename %q for sheet %s", name, sheet.DisplayName())
		}
		assigned[sheet], used[name] = name, true
	}
	return assigned, nil
}
