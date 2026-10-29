package main

import (
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestSymbolXform(t *testing.T) {
	for _, tc := range []struct {
		sym    string
		x, y   float64 // library point (Y up)
		px, py float64 // page point (Y down)
	}{
		{`(symbol (at 10 20 0))`, 1, 2, 11, 18},
		{`(symbol (at 10 20 90))`, 1, 0, 10, 19}, // counterclockwise: right turns into up
		{`(symbol (at 10 20 180))`, 1, 2, 9, 22},
		{`(symbol (at 10 20 0) (mirror y))`, 1, 2, 9, 18},
		{`(symbol (at 10 20 0) (mirror x))`, 1, 2, 11, 22},
		{`(symbol (at 10 20 90) (mirror x))`, 1, 2, 8, 21}, // as kicad-cli draws it
		{`(symbol (at 10 20 270) (mirror y))`, 1, 2, 8, 21},
	} {
		n, err := ParseSexpr(tc.sym)
		if err != nil {
			t.Fatal(err)
		}
		lib, _ := symbolXform(n)
		if x, y := lib.pt(tc.x, tc.y); math.Abs(x-tc.px) > 1e-9 || math.Abs(y-tc.py) > 1e-9 {
			t.Errorf("%s: (%g, %g) -> (%g, %g), want (%g, %g)", tc.sym, tc.x, tc.y, x, y, tc.px, tc.py)
		}
	}
}

func TestOrientKeepsTextUpright(t *testing.T) {
	for _, tc := range []struct {
		t            xform
		angle        float64
		hj, vj       int
		wantA        float64
		wantH, wantV int
	}{
		{identity, 0, -1, 1, 0, -1, 1},
		{xform{a: -1, d: 1}, 0, -1, 1, 0, 1, 1},               // mirrored left-right
		{xform{a: -1, d: -1}, 0, -1, 1, 0, 1, -1},             // rotated by 180°
		{xform{a: 0, b: 1, c: -1, d: 0}, 0, -1, 0, 90, -1, 0}, // rotated by 90°
		{xform{a: 0, b: 1, c: -1, d: 0}, 90, -1, 0, 0, 1, 0},
	} {
		got := orient(textStyle{angle: tc.angle, hj: tc.hj, vj: tc.vj}, tc.t)
		if got.angle != tc.wantA || got.hj != tc.wantH || got.vj != tc.wantV {
			t.Errorf("%+v angle %g: got angle %g, justify %d %d", tc.t, tc.angle, got.angle, got.hj, got.vj)
		}
	}
}

func TestArcTo(t *testing.T) {
	// A half circle through the bottom (Y down) turns clockwise on screen.
	if got := arcTo(1, 0, 0, 1, -1, 0); got != "A1 1 0 0 1 -1 0" {
		t.Errorf("lower half: %s", got)
	}
	if got := arcTo(1, 0, 0, -1, -1, 0); got != "A1 1 0 0 0 -1 0" {
		t.Errorf("upper half: %s", got)
	}
	// Three quarters of a circle need the large arc.
	if got := arcTo(1, 0, -1, 0, 0, -1); got != "A1 1 0 1 1 0 -1" {
		t.Errorf("three quarters: %s", got)
	}
	if got := arcTo(0, 0, 1, 1, 2, 2); got != "L2 2" {
		t.Errorf("collinear: %s", got)
	}
}

func TestKicadText(t *testing.T) {
	for in, want := range map[string]string{
		"~{RESET}/A{slash}B_{n}": "RESET/A/Bn",
		"~{A{brace}B}":           "A{B", // an escaped brace does not end the overbar
		"{unknown}":              "{unknown}",
		"a}b{":                   "a}b{",
	} {
		if got := kicadText(in); got != want {
			t.Errorf("kicadText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderBuiltinSchematic(t *testing.T) {
	src := memSource{
		"test.kicad_sch": `(kicad_sch (version 20231120) (uuid "root") (paper "A3")
  (title_block (title "Builtin & <test>"))
  (lib_symbols
    (symbol "Device:R" (pin_numbers (hide yes)) (pin_names (offset 0))
      (property "Reference" "R" (at 2 0 90) (effects (font (size 1.27 1.27))))
      (symbol "R_0_1" (rectangle (start -1 -2.5) (end 1 2.5) (stroke (width 0.254) (type default)) (fill (type none))))
      (symbol "R_1_1"
        (pin passive line (at 0 3.81 270) (length 1.27) (name "~" (effects (font (size 1.27 1.27)))) (number "1" (effects (font (size 1.27 1.27)))))
        (pin passive line (at 0 -3.81 90) (length 1.27) (name "~" (effects (font (size 1.27 1.27)))) (number "2" (effects (font (size 1.27 1.27))))))))
  (wire (pts (xy 10 20) (xy 30 20)) (stroke (width 0) (type default)) (uuid "w1"))
  (label "NET<1>" (at 10 20 0) (effects (font (size 1.27 1.27)) (justify left bottom)) (uuid "l1"))
  (global_label "VBUS" (shape input) (at 30 20 180) (effects (font (size 1.27 1.27)) (justify right)) (uuid "g1"))
  (symbol (lib_id "Device:R") (at 50 50 90) (unit 1) (uuid "r1")
    (property "Reference" "R7" (at 52 50 90) (effects (font (size 1.27 1.27))))
    (property "Value" "10k" (at 48 50 90) (effects (font (size 1.27 1.27))))
    (property "Footprint" "R_0603" (at 50 50 0) (effects (font (size 1.27 1.27)) (hide yes)))
    (instances (project "test" (path "/root" (reference "R9") (unit 1)))))
  (sheet_instances (path "/" (page "1"))))`,
	}
	p, err := LoadProject(src, "test.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	pages, err := Render(p, t.TempDir(), RenderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	rp := pages["/root"]
	if rp == nil || rp.Width != 420 || rp.Height != 297 {
		t.Fatalf("page: %+v", rp)
	}
	svg := string(rp.SVG)
	for _, want := range []string{
		`viewBox="0 0 420 297"`,
		`>NET&lt;1&gt;<`,                    // escaped label text
		`>R9<`,                              // the per-instance reference, not the stored one
		`>10k<`,                             // visible value
		`Title: Builtin &amp; &lt;test&gt;`, // drawing sheet
		`d="M10 20L30 20"`,                  // the wire
		`stroke="` + colGlobal + `"`,        // global label outline
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG lacks %s", want)
		}
	}
	// Hidden fields, hidden pin numbers and the "~" placeholder name.
	for _, unwanted := range []string{">R_0603<", ">R7<", `fill="` + colPinNum + `"`, ">~<"} {
		if strings.Contains(svg, unwanted) {
			t.Errorf("SVG contains hidden text %s", unwanted)
		}
	}
	if readSVG := viewBoxRe.FindSubmatch(rp.SVG); readSVG == nil {
		t.Error("viewBox not readable by readSVG")
	}

	pages, _ = Render(p, t.TempDir(), RenderOptions{ExcludeDrawSheet: true})
	if strings.Contains(string(pages["/root"].SVG), "Title:") {
		t.Error("drawing sheet drawn despite ExcludeDrawSheet")
	}
}

func TestRenderBuiltinPCB(t *testing.T) {
	b := board(t, "{AT}", "50 50 90", "{PADROT}", "90")
	pages := planPages(b, b)
	rendered, err := RenderPCB(b, pages, t.TempDir(), RenderOptions{ExcludeDrawSheet: true})
	if err != nil {
		t.Fatal(err)
	}
	all := rendered["all"]
	if all == nil || len(all.Layers) != 4 || all.Width != 297 || all.Height != 210 {
		t.Fatalf("overview: %+v", all)
	}
	fcu := rendered["F.Cu"]
	if fcu == nil {
		t.Fatal("no F.Cu page")
	}
	svg := string(fcu.SVG)
	for _, want := range []string{
		`d="M10 10L20 10" stroke="` + pcbColors["F.Cu"] + `" stroke-width="0.25"`, // the track
		`translate(50 50.8) rotate(-90)`,                                          // pad 1 at (-0.8, 0) turns down with the footprint rotated by 90°
		circlePath(30, 30, 0.3) + circlePath(30, 30, 0.15),                        // the via with its drill hole
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("F.Cu lacks %s", want)
		}
	}
	silk := ""
	for _, l := range all.Layers {
		if l.Layer == "F.SilkS" {
			silk = string(l.SVG)
		}
	}
	if !strings.Contains(silk, ">R1<") || strings.Contains(silk, ">10k<") {
		t.Errorf("silkscreen should show the reference only:\n%s", silk)
	}
	if _, ok := rendered["B.Cu"]; !ok {
		t.Error("no B.Cu page")
	}
}

func TestRenderBuiltinMultiUnit(t *testing.T) {
	src := memSource{"test.kicad_sch": `(kicad_sch (version 20231120) (uuid "root") (paper "A4")
  (lib_symbols
    (symbol "X:Dual"
      (symbol "Dual_0_1" (polyline (pts (xy 0 0) (xy 0 9)) (stroke (width 0) (type default)) (fill (type none))))
      (symbol "Dual_1_1" (polyline (pts (xy 0 0) (xy 1 1)) (stroke (width 0) (type default)) (fill (type none))))
      (symbol "Dual_2_1" (polyline (pts (xy 0 0) (xy 2 2)) (stroke (width 0) (type default)) (fill (type none))))))
  (symbol (lib_id "X:Dual") (at 50 50 0) (unit 2) (uuid "u1")
    (property "Reference" "U1" (at 50 45 0) (effects (font (size 1.27 1.27))))
    (instances (project "test" (path "/root" (reference "U1") (unit 2)))))
  (sheet_instances (path "/" (page "1"))))`}
	p, err := LoadProject(src, "test.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	pages, err := Render(p, t.TempDir(), RenderOptions{Builtin: true, KicadCLI: "/nonexistent", ExcludeDrawSheet: true})
	if err != nil {
		t.Fatal(err)
	}
	svg := string(pages["/root"].SVG)
	for _, want := range []string{">U1B<", `d="M50 50L52 48"`, `d="M50 50L50 41"`} {
		if !strings.Contains(svg, want) {
			t.Errorf("SVG lacks %s", want)
		}
	}
	if strings.Contains(svg, `d="M50 50L51 49"`) {
		t.Error("unit 1 drawn for unit 2")
	}
}

func TestRenderBuiltinPads(t *testing.T) {
	b, err := LoadBoard(memSource{"b.kicad_pcb": `(kicad_pcb (version 20240108) (generator "pcbnew")
  (layers (0 "F.Cu" signal) (31 "B.Cu" signal) (44 "Edge.Cuts" user))
  (net 0 "")
  (footprint "X:TH" (layer "F.Cu") (at 40 40 90) (uuid "fp1")
    (pad "1" thru_hole oval (at 0 0 90) (size 2 3) (drill oval 1 1.5 (offset 0.2 0)) (layers "*.Cu" "*.Mask") (uuid "p1"))
    (pad "2" smd custom (at 2 0 90) (size 1 1) (layers "F.Cu") (options (clearance outline) (anchor circle))
      (primitives (gr_poly (pts (xy 0 0) (xy 1 0) (xy 1 1)) (width 0) (fill yes))) (uuid "p2"))
    (zone (net 0) (net_name "") (layer "F.Cu") (uuid "z1")
      (polygon (pts (xy 60 60) (xy 61 60) (xy 61 61)))
      (filled_polygon (layer "F.Cu") (pts (xy 60 60) (xy 61 60) (xy 61 61)))))
  (gr_text "${REFERENCE}" (at 10 10 0) (layer "F.Cu") (uuid "t1") (effects (font (size 1 1)))))`}, "b.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	col := pcbColors["F.Cu"]
	svg := string(renderBoardBuiltin(b, []string{"F.Cu"}, false).SVG)
	for _, want := range []string{
		`<g data-layer="F.Cu">`,
		// The oval pad with its offset oval hole, at the footprint's origin.
		`translate(40 40) rotate(-90)"><path d="` + roundRect(2, 3, 1) + roundRectAt(0.2, 0, 1, 1.5, 0.5) + `" fill="` + col,
		// The custom pad: its circular anchor and its primitive, at (2, 0) turned with the footprint.
		`translate(40 38) rotate(-90)"><path d="` + circlePath(0, 0, 0.5) + `"`,
		`<path d="M0 0L1 0L1 1Z" fill="` + col + `"`,
		`<path d="M60 60L61 60L61 61Z" fill="` + col + `"`, // the footprint's zone fill
		`>${REFERENCE}<`, // board text keeps its variables
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("F.Cu lacks %s\n%s", want, svg)
		}
	}
	back := string(renderBoardBuiltin(b, []string{"B.Cu"}, false).SVG)
	if !strings.Contains(back, roundRectAt(0.2, 0, 1, 1.5, 0.5)) || strings.Contains(back, "M60 60") {
		t.Errorf("B.Cu should have the through-hole pad only:\n%s", back)
	}
}

// Without kicad-cli, run uses the built-in renderer and says so.
func TestRunFallsBackToBuiltin(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	bin := t.TempDir()
	if err := os.Symlink(gitPath, filepath.Join(bin, "git")); err != nil {
		t.Skip(err)
	}
	t.Setenv("PATH", bin)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.kicad_sch"),
		[]byte(`(kicad_sch (uuid root) (paper "A4") (wire (pts (xy 10 10) (xy 20 10))) (sheet_instances (path "/" (page "1"))))`), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(dir, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	for _, tc := range []struct {
		name      string
		opt       RenderOptions
		want, not []string
	}{
		{"missing", RenderOptions{}, []string{"kicad-cli not found"}, []string{"-theme"}},
		{"theme", RenderOptions{Theme: "dark"}, []string{"kicad-cli not found", "-theme needs kicad-cli"}, nil},
		{"builtin", RenderOptions{Builtin: true, KicadCLI: "/nonexistent/kicad-cli"}, []string{"-builtin ignores -kicad-cli"}, []string{"not found"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "report.html")
			stderr := captureStderr(t, func() {
				if err := run("", "", "test.kicad_sch", out, false, false, false, tc.opt); err != nil {
					t.Fatal(err)
				}
			})
			for _, w := range tc.want {
				if !strings.Contains(stderr, w) {
					t.Errorf("stderr lacks %q: %s", w, stderr)
				}
			}
			for _, w := range tc.not {
				if strings.Contains(stderr, w) {
					t.Errorf("stderr has %q: %s", w, stderr)
				}
			}
			if _, err := os.Stat(out); err != nil {
				t.Errorf("no report: %v", err)
			}
		})
	}
}

func captureStderr(t *testing.T, f func()) (output string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		r.Close()
		done <- string(b)
	}()
	// Fatal calls Goexit, so cleanup must run even when f never returns.
	defer func() {
		os.Stderr = old
		w.Close()
		output = <-done
	}()
	f()
	return
}

func TestCaptureStderrCleanup(t *testing.T) {
	old := os.Stderr
	for _, exit := range []string{"return", "panic", "Goexit"} {
		t.Run(exit, func(t *testing.T) {
			var captured *os.File
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer func() { _ = recover() }()
				output := captureStderr(t, func() {
					captured = os.Stderr
					_, _ = io.WriteString(os.Stderr, "captured output")
					switch exit {
					case "panic":
						panic("callback failed")
					case "Goexit":
						runtime.Goexit() // the mechanism used by t.Fatal
					}
				})
				if output != "captured output" {
					t.Errorf("captured %q", output)
				}
			}()
			<-finished
			if os.Stderr != old {
				t.Error("stderr was not restored")
			}
			if _, err := captured.Stat(); !errors.Is(err, os.ErrClosed) {
				t.Errorf("capture writer remains open: %v", err)
			}
		})
	}
}

var pathDataRe = regexp.MustCompile(`\sd="([^"]*)"`)
var pathTokenRe = regexp.MustCompile(`[A-Za-z]|[-+]?(?:\d+(?:\.\d*)?|\.\d+)(?:[eE][-+]?\d+)?`)

// lineSubpaths compares the ordered vertices of each absolute M/L subpath,
// rounded to 0.01 mm. Sorting subpaths allows different drawing orders but
// preserves connectivity and move boundaries. Unsupported commands fail the
// test instead of silently treating curve parameters as coordinates.
func lineSubpaths(t *testing.T, svg []byte) []string {
	t.Helper()
	var paths []string
	for _, m := range pathDataRe.FindAllSubmatch(svg, -1) {
		tokens := pathTokenRe.FindAllString(string(m[1]), -1)
		var points []string
		flush := func(closed bool) {
			if len(points) == 0 {
				return
			}
			kind := "open:"
			if closed {
				kind = "closed:"
			}
			paths = append(paths, kind+strings.Join(points, ";"))
			points = nil
		}
		command := ""
		for i := 0; i < len(tokens); {
			switch tokens[i] {
			case "M", "L":
				command = tokens[i]
				i++
				if command == "M" {
					flush(false)
				}
			case "Z", "z":
				flush(true)
				command = ""
				i++
				continue
			}
			if command == "" || i+1 >= len(tokens) {
				t.Fatalf("unsupported line path %q", m[1])
			}
			x, errX := strconv.ParseFloat(tokens[i], 64)
			y, errY := strconv.ParseFloat(tokens[i+1], 64)
			if errX != nil || errY != nil {
				t.Fatalf("unsupported command or coordinate in line path %q", m[1])
			}
			points = append(points, ff(math.Round(x*100)/100)+" "+ff(math.Round(y*100)/100))
			i += 2
			command = "L" // repeated pairs after M are implicit lines
		}
		flush(false)
	}
	sort.Strings(paths)
	return paths
}

func TestLineSubpathsPreserveConnectivity(t *testing.T) {
	original := `<path d="M0 0L1 0L1 1"/>`
	for _, different := range []string{
		`<path d="M0 0L1 1L1 0"/>`,  // same vertices, different edges
		`<path d="M0 0L1 0M1 1"/>`,  // same vertices, disconnected subpath
		`<path d="M0 0L1 0L1 1Z"/>`, // closing edge must be preserved
	} {
		if strings.Join(lineSubpaths(t, []byte(original)), "|") == strings.Join(lineSubpaths(t, []byte(different)), "|") {
			t.Errorf("comparison equates %s with %s", original, different)
		}
	}
}

// Symbol placement in every orientation, compared with kicad-cli's own plot.
func TestBuiltinMatchesKiCadGeometry(t *testing.T) {
	cli := kicadCLI(t)
	sch := `(kicad_sch (version 20231120) (generator "eeschema") (uuid "00000000-0000-0000-0000-000000000001") (paper "A4")
  (lib_symbols
    (symbol "X:L" (pin_numbers (hide yes)) (pin_names (hide yes)) (in_bom yes) (on_board yes)
      (property "Reference" "U" (at 0 0 0) (effects (font (size 1.27 1.27)) (hide yes)))
      (property "Value" "L" (at 0 0 0) (effects (font (size 1.27 1.27)) (hide yes)))
      (symbol "L_1_1" (polyline (pts (xy 0 0) (xy 6 0) (xy 6 3)) (stroke (width 0.254) (type default)) (fill (type none))))))
  (wire (pts (xy 20 180) (xy 60 180)) (stroke (width 0) (type default)) (uuid "00000000-0000-0000-0000-0000000000ff"))
`
	i := 0
	for _, mirror := range []string{"", "(mirror x)", "(mirror y)"} {
		for _, rot := range []string{"0", "90", "180", "270"} {
			i++
			uuid := "00000000-0000-0000-0000-0000000001" + strings.Repeat("0", 2-len(ff(float64(i)))) + ff(float64(i))
			sch += `  (symbol (lib_id "X:L") (at ` + ff(float64(20+(i-1)%4*40)) + " " + ff(float64(30+(i-1)/4*40)) + " " + rot + `) ` + mirror +
				` (unit 1) (in_bom yes) (on_board yes) (uuid "` + uuid + `")
    (property "Reference" "U` + ff(float64(i)) + `" (at 0 0 0) (effects (font (size 1.27 1.27)) (hide yes)))
    (property "Value" "L" (at 0 0 0) (effects (font (size 1.27 1.27)) (hide yes)))
    (instances (project "t" (path "/00000000-0000-0000-0000-000000000001" (reference "U` + ff(float64(i)) + `") (unit 1)))))
`
		}
	}
	sch += `  (sheet_instances (path "/" (page "1"))))`
	p, err := LoadProject(memSource{"t.kicad_sch": sch}, "t.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	opt := RenderOptions{KicadCLI: cli, ExcludeDrawSheet: true}
	kicad, err := Render(p, t.TempDir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	opt.Builtin = true
	builtin, err := Render(p, t.TempDir(), opt)
	if err != nil {
		t.Fatal(err)
	}
	key := p.Sheets[0].Key()
	want, got := lineSubpaths(t, kicad[key].SVG), lineSubpaths(t, builtin[key].SVG)
	if strings.Join(got, ";") != strings.Join(want, ";") {
		t.Errorf("subpaths differ:\nbuilt-in %v\nkicad-cli %v", got, want)
	}
}
