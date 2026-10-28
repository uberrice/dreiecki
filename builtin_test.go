package main

import (
	"math"
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
	if got := kicadText("~{RESET}/A{slash}B_{n}"); got != "RESET/A/Bn" {
		t.Errorf("got %q", got)
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
