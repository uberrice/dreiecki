package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestViewReportsSkipSemanticExtraction(t *testing.T) {
	// These objects have no parsed nodes, so any extraction would panic.
	p := &Project{Sheets: []*SheetInst{{UUIDPath: "/root", File: "root.kicad_sch"}}}
	renders := map[string]*RenderedPage{"/root": {SVG: []byte("<svg/>"), Width: 100, Height: 80}}
	sch := buildReport(p, p, renders, renders)
	b := &Board{Project: &Project{Root: "root.kicad_pcb"}, Layers: []string{"F.Cu"}}
	pcbRenders := map[string]*RenderedPage{"F.Cu": renders["/root"], "all": {Width: 100, Height: 80, Layers: []*LayerSVG{{Layer: "F.Cu", SVG: []byte("<svg/>")}}}}
	pcb := buildPCBReport(b, b, []pcbPage{{ID: "all", Stack: true, Layers: []string{"F.Cu"}}, {ID: "F.Cu", Layer: "F.Cu"}}, pcbRenders, pcbRenders)
	for _, rep := range []*Report{sch, pcb} {
		if !rep.View {
			t.Fatal("missing view flag")
		}
		for _, page := range rep.Pages {
			if page.Status != "same" || len(page.Changes) != 0 || page.SvgA != "" {
				t.Fatalf("unexpected view page: %+v", page)
			}
			for _, layer := range page.Stack {
				if layer.SvgA != "" || layer.SvgB == "" {
					t.Fatalf("unexpected view stack: %+v", layer)
				}
			}
		}
	}
}

func TestPackSVGWriterReuse(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// The expected result is spelled out: packSVG must drop the volatile title, and the
			// expectation must not be computed with the function under test.
			for _, tc := range []struct{ in, want string }{
				{"<svg><title>volatile</title>first</svg>", "<svg>first</svg>"},
				{"<svg>second</svg>", "<svg>second</svg>"},
			} {
				packed, err := base64.StdEncoding.DecodeString(packSVG([]byte(tc.in)))
				if err != nil {
					t.Error(err)
					return
				}
				reader, err := gzip.NewReader(bytes.NewReader(packed))
				if err != nil {
					t.Error(err)
					return
				}
				got, err := io.ReadAll(reader)
				reader.Close()
				if err != nil || !bytes.Equal(got, []byte(tc.want)) {
					t.Errorf("unexpected packed SVG: %q, %v; want %q", got, err, tc.want)
				}
			}
		}()
	}
	wg.Wait()
}

func TestWriteReportEscapingAndFailedReplacement(t *testing.T) {
	out := filepath.Join(t.TempDir(), "report.html")
	rep := &Report{Kind: "sch", View: true, Root: "</script><script>alert(1)</script>", B: RevInfo{Label: "<&>"}, Pages: []PageReport{{ID: "page", SvgB: "packed"}}}
	if err := writeReport(rep, out); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	html := string(original)
	if !strings.Contains(html, "<title>Schematic: &lt;&amp;&gt;</title>") || strings.Contains(html, rep.Root) {
		t.Fatal("HTML escaping lost")
	}
	start := strings.Index(html, `{"kind":`)
	if start < 0 {
		t.Fatal("report data missing")
	}
	var decoded Report
	if err := json.NewDecoder(strings.NewReader(html[start:])).Decode(&decoded); err != nil || decoded.Root != rep.Root {
		t.Fatalf("report did not round trip: %+v, %v", decoded, err)
	}
	rep.Pages[0].Width = math.NaN()
	if err := writeReport(rep, out); err == nil {
		t.Fatal("invalid JSON should fail")
	}
	after, err := os.ReadFile(out)
	if err != nil || !bytes.Equal(after, original) {
		t.Fatal("failed report overwrote existing report")
	}
	files, err := os.ReadDir(filepath.Dir(out))
	if err != nil || len(files) != 1 {
		t.Fatal("temporary report leaked")
	}
	if err := writeReport(&Report{}, filepath.Join(filepath.Dir(out), "missing", "report.html")); err == nil {
		t.Fatal("missing directory should fail")
	}
}

func TestDiffItemsFallbackPreservesUUIDPriorityAndMultiplicity(t *testing.T) {
	a := []*Item{{UUID: "kept", Kind: "wire", Content: "old"}, {UUID: "new-a", Kind: "wire", Content: "same"}, {UUID: "new-b", Kind: "wire", Content: "same"}, {UUID: "new-c", Kind: "wire", Content: "same"}}
	b := []*Item{{UUID: "kept", Kind: "wire", Content: "same"}, {UUID: "other-a", Kind: "wire", Content: "same"}, {UUID: "other-b", Kind: "wire", Content: "same"}}
	changes := diffItems(a, b)
	if len(changes) != 2 || changes[0].Op != "changed" || changes[1].Op != "removed" {
		t.Fatalf("UUID match or content multiplicity lost: %+v", changes)
	}
}

func TestRenderPCBBatchWithKiCad(t *testing.T) {
	cli := kicadCLI(t)
	help, err := exec.Command(cli, "pcb", "export", "svg", "--help").Output()
	if err != nil || !bytes.Contains(help, []byte("--mode-multi")) {
		t.Skip("KiCad does not support multi-file plotting")
	}
	const data = `(kicad_pcb (version 20240108) (generator "pcbnew")
 (general (thickness 1.6)) (paper "A4")
 (layers (0 "F.Cu" signal) (31 "B.Cu" signal) (37 "F.SilkS" user "F.Silkscreen") (44 "Edge.Cuts" user))
 (net 0 "")
 (segment (start 15 15) (end 20 20) (width 0.25) (layer "F.Cu") (net 0) (uuid "00000000-0000-0000-0000-000000000001"))
 (gr_rect (start 10 10) (end 40 40) (stroke (width 0.05) (type default)) (fill none) (layer "Edge.Cuts") (uuid "00000000-0000-0000-0000-000000000002"))
 (gr_line (start 12 12) (end 25 25) (stroke (width 0.15) (type default)) (layer "F.SilkS") (uuid "00000000-0000-0000-0000-000000000003")))`
	board, err := LoadBoard(memSource{"fixture.kicad_pcb": data}, "fixture.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	file, err := board.Materialize(filepath.Join(work, "src"))
	if err != nil {
		t.Fatal(err)
	}
	pages := planPages(board, board)
	batch, ok := renderPCBBatch(board, pages, file, filepath.Join(work, "batch"), RenderOptions{KicadCLI: cli, ExcludeDrawSheet: true})
	if !ok {
		t.Fatal("batch export failed")
	}
	check := func(layers []string, rp *RenderedPage) {
		t.Helper()
		out := filepath.Join(work, "single.svg")
		args := []string{"pcb", "export", "svg", "--mode-single", "--exclude-drawing-sheet", "-l", strings.Join(layers, ","), "-o", out, file}
		if output, err := exec.Command(cli, args...).CombinedOutput(); err != nil {
			t.Fatalf("single export: %v, %s", err, output)
		}
		single, err := readSVG(out)
		if err != nil {
			t.Fatal(err)
		}
		if rp == nil || single.Width != rp.Width || single.Height != rp.Height || !bytes.Equal(stripVolatile(single.SVG), stripVolatile(rp.SVG)) {
			t.Fatalf("batch differs from single export for %v", layers)
		}
	}
	for _, pg := range pages {
		if pg.Stack {
			for _, layer := range batch[pg.ID].Layers {
				check([]string{layer.Layer}, &RenderedPage{SVG: layer.SVG, Width: batch[pg.ID].Width, Height: batch[pg.ID].Height})
			}
		} else {
			check(pg.Layers, batch[pg.ID])
		}
	}
}

func TestRenderPCBBatchSelectionAndLegacyFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI requires a POSIX shell")
	}
	for _, tc := range []struct {
		name                string
		exclude, supported  bool
		wantBatch, wantHelp int
	}{
		{"legacy", true, false, 0, 1},
		{"frame-free", true, true, 2, 1},
		{"drawing-sheet", false, true, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cli := filepath.Join(dir, "kicad-cli")
			supported := ""
			if tc.supported {
				supported = "--mode-multi --common-layers"
			}
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$0.calls"
if [ "$4" = "--help" ]; then
 printf '%s\n' '` + supported + `'
 exit 0
fi
multi=0
layers=''
while [ "$#" -gt 0 ]; do
 case "$1" in
  --mode-multi) multi=1 ;;
  -o) shift; out="$1" ;;
  -l) shift; layers="$1" ;;
 esac
 shift
done
if [ "$multi" = 1 ]; then
 mkdir -p "$out"
 oldIFS="$IFS"; IFS=','
 for layer in $layers; do
  if [ "$layer" = F.SilkS ]; then layer=F.Silkscreen; fi
  name=$(printf '%s' "$layer" | tr '.' '_')
  printf '%s' '<svg viewBox="0 0 100 80"/>' > "$out/b-$name.svg"
 done
 IFS="$oldIFS"
else
 printf '%s' '<svg viewBox="0 0 100 80"/>' > "$out"
fi
`
			// Keep the call log beside the CLI, so no shell interpolation is needed.
			log := cli + ".calls"
			if err := os.WriteFile(cli, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			b := board(t)
			pages := planPages(b, b)
			renders, err := RenderPCB(b, pages, filepath.Join(dir, "render"), RenderOptions{KicadCLI: cli, ExcludeDrawSheet: tc.exclude, Theme: "perf-test"})
			if err != nil {
				t.Fatal(err)
			}
			if len(renders) != len(pages) {
				t.Fatalf("missing pages: got %d want %d", len(renders), len(pages))
			}
			calls, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(string(calls), "--mode-multi"); got != tc.wantBatch {
				t.Fatalf("batch calls: got %d want %d\n%s", got, tc.wantBatch, calls)
			}
			plots := strings.Count(string(calls), "\n") - tc.wantHelp
			if got := strings.Count(string(calls), "--theme perf-test"); got != plots {
				t.Fatalf("theme not passed to every plot: %s", calls)
			}
			if got := strings.Count(string(calls), "--help"); got != tc.wantHelp {
				t.Fatalf("help calls: got %d want %d\n%s", got, tc.wantHelp, calls)
			}
		})
	}
}

type countingSource struct {
	memSource
	reads map[string]int
}

func (s countingSource) ReadFile(rel string) ([]byte, error) {
	s.reads[rel]++
	return s.memSource.ReadFile(rel)
}

func TestLoadProjectReadsRootOnce(t *testing.T) {
	src := countingSource{memSource: memSource{"root.kicad_sch": `(kicad_sch (uuid "root") (lib_symbols))`}, reads: map[string]int{}}
	if _, err := LoadProject(src, "root.kicad_sch"); err != nil {
		t.Fatal(err)
	}
	if got := src.reads["root.kicad_sch"]; got != 1 {
		t.Fatalf("root read %d times", got)
	}
}
