package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestBuiltinDNP(t *testing.T) {
	const sch = `(kicad_sch (uuid root) (paper "A4")
 (lib_symbols (symbol "X:R" (symbol "R_1_1" (rectangle (start -1 -2) (end 1 2) (stroke (width 0.15)) (fill (type none))))))
 (symbol (lib_id "X:R") (at 50 50 90) (unit 1) (dnp DNP) (property "Reference" "R1" (at 50 45 0)) (uuid r)))`
	render := func(flag string) []byte {
		p, err := LoadProject(memSource{"t.kicad_sch": strings.ReplaceAll(sch, "DNP", flag)}, "t.kicad_sch")
		if err != nil {
			t.Fatal(err)
		}
		return renderSheetBuiltin(p, p.Sheets[0], false).SVG
	}
	normal, dnp := render("no"), render("yes")
	if bytes.Equal(normal, dnp) || !bytes.Contains(dnp, []byte(`data-dnp="yes"`)) || bytes.Contains(normal, []byte(`data-dnp`)) {
		t.Fatal("DNP marker missing or drawn on populated symbol")
	}
	// Body corners follow the 90 degree library-to-page transform.
	if !bytes.Contains(dnp, []byte(`M52.5 51.5L47.5 48.5`)) {
		t.Fatalf("DNP marker does not span rotated body:\n%s", dnp)
	}
}

func marginBoard(t *testing.T, setup, fp, pad string) *Board {
	t.Helper()
	src := `(kicad_pcb (version 20260101) (layers (0 "F.Cu" signal) (31 "B.Cu" signal) (35 "F.Paste" user) (39 "F.Mask" user))
 (setup ` + setup + `) (footprint "X" (layer "F.Cu") (at 50 50) ` + fp + `
 (pad "1" smd rect (at 0 0) (size 2 4) (layers "F.Cu" "F.Mask" "F.Paste") ` + pad + `)))`
	b, err := LoadBoard(memSource{"b.kicad_pcb": src}, "b.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestBuiltinPadMargins(t *testing.T) {
	for _, tc := range []struct {
		name, setup, fp, pad, pro    string
		maskW, maskH, pasteW, pasteH float64
	}{
		{name: "defaults", maskW: 2, maskH: 4, pasteW: 2, pasteH: 4},
		{name: "board", setup: `(pad_to_mask_clearance 0.3) (pad_to_paste_clearance 0.1) (pad_to_paste_clearance_ratio -0.1)`, maskW: 2.6, maskH: 4.6, pasteW: 1.8, pasteH: 3.4},
		{name: "footprint", setup: `(pad_to_mask_clearance 0.3)`, fp: `(solder_mask_margin 0.2) (solder_paste_margin 0.1) (solder_paste_ratio -0.1)`, maskW: 2.4, maskH: 4.4, pasteW: 1.8, pasteH: 3.4},
		{name: "pad", fp: `(solder_mask_margin 0.2) (solder_paste_ratio -0.1)`, pad: `(solder_mask_margin 0.4) (solder_paste_margin 0.1) (solder_paste_margin_ratio -0.05)`, maskW: 2.8, maskH: 4.8, pasteW: 2, pasteH: 3.8},
		{name: "explicit zero", fp: `(solder_mask_margin 0.2) (solder_paste_margin 0.2) (solder_paste_ratio -0.1)`, pad: `(solder_mask_margin 0) (solder_paste_margin 0) (solder_paste_margin_ratio 0)`, maskW: 2, maskH: 4, pasteW: 2, pasteH: 4},
		{name: "project", setup: `(pad_to_mask_clearance 0.8)`, pro: `{"board":{"design_settings":{"rules":{"solder_mask_clearance":0.1,"solder_paste_clearance":0.2,"solder_paste_margin_ratio":-0.05}}}}`, maskW: 2.2, maskH: 4.2, pasteW: 2.2, pasteH: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := marginBoard(t, tc.setup, tc.fp, tc.pad)
			if tc.pro != "" {
				b.Files["b.kicad_pro"] = []byte(tc.pro)
			}
			for _, layer := range []struct {
				name string
				w, h float64
			}{{"F.Mask", tc.maskW, tc.maskH}, {"F.Paste", tc.pasteW, tc.pasteH}, {"F.Cu", 2, 4}} {
				svg := renderBoardBuiltin(b, []string{layer.name}, false).SVG
				if !bytes.Contains(svg, []byte(`d="`+roundRect(layer.w, layer.h, 0)+`"`)) {
					t.Errorf("%s dimensions want %g x %g:\n%s", layer.name, layer.w, layer.h, svg)
				}
			}
		})
	}
	b := marginBoard(t, "", "", `(solder_paste_margin -3)`)
	if bytes.Contains(renderBoardBuiltin(b, []string{"F.Paste"}, false).SVG, []byte("<path")) {
		t.Error("collapsed paste aperture was rendered")
	}
}

func TestBuiltinBoardTextBoxes(t *testing.T) {
	src := `(kicad_pcb (layers (0 "F.Cu" signal) (37 "F.SilkS" user))
 (gr_text_box "CONTENT" (start 10 20) (end 20 30) (margins 1 2 1 2) (layer "F.SilkS") (effects (font (size 1 1)) (justify left top)) (border yes) (stroke (width 0.2) (type default)))
 (footprint "X" (layer "F.Cu") (at 50 50 90) (property "Reference" "U1")
 (fp_text_box "${REFERENCE}" (start 0 0) (end 8 4) (layer "F.SilkS") (effects (font (size 1 1))) (border no) (stroke (width 0.1)))))`
	render := func(content, layer string) []byte {
		b, err := LoadBoard(memSource{"b.kicad_pcb": strings.ReplaceAll(src, "CONTENT", content)}, "b.kicad_pcb")
		if err != nil {
			t.Fatal(err)
		}
		return renderBoardBuiltin(b, []string{layer}, false).SVG
	}
	a, b := render("hello box", "F.SilkS"), render("edited box", "F.SilkS")
	if bytes.Equal(a, b) {
		t.Fatal("text box edit does not affect SVG")
	}
	for _, want := range []string{`d="M10 20L20 20L20 30L10 30Z"`, `translate(11 22)`, `>hello box<`, `>U1<`, `rotate(-90)`} {
		if !bytes.Contains(a, []byte(want)) {
			t.Errorf("missing %s:\n%s", want, a)
		}
	}
	if bytes.Contains(a, []byte(`M50 50L50 42L54 42L54 50Z`)) {
		t.Error("border no ignored")
	}
	if bytes.Contains(render("hello box", "F.Cu"), []byte("hello box")) {
		t.Error("text box drawn on wrong layer")
	}
	if got := wrapText("one two three\nfour", 5, 1); got != "one\ntwo\nthree\nfour" {
		t.Errorf("wrap: %q", got)
	}
}

func TestBuiltinLegacyZeroMarginsInherit(t *testing.T) {
	b := marginBoard(t, `(pad_to_mask_clearance 0.3) (pad_to_paste_clearance 0.1) (pad_to_paste_clearance_ratio -0.1)`, `(solder_mask_margin 0.2) (solder_paste_margin 0) (solder_paste_margin_ratio 0)`, `(solder_mask_margin 0) (solder_paste_margin 0) (solder_paste_margin_ratio 0)`)
	b.Node.Child("version").List[1].Atom = "20240108"
	mask := renderBoardBuiltin(b, []string{"F.Mask"}, false).SVG
	paste := renderBoardBuiltin(b, []string{"F.Paste"}, false).SVG
	if !bytes.Contains(mask, []byte(roundRect(2.4, 4.4, 0))) || !bytes.Contains(paste, []byte(roundRect(1.8, 3.4, 0))) {
		t.Fatalf("legacy zero values did not inherit:\n%s\n%s", mask, paste)
	}
}

func TestBuiltinFootprintTextBoxLocalAngle(t *testing.T) {
	// KiCad serializes absolute text angle 180 on a 90-degree footprint as angle 90.
	src := `(kicad_pcb (layers (0 "F.Cu" signal) (37 "F.SilkS" user))
 (footprint "X" (layer "F.Cu") (at 50 50 90)
 (fp_text_box "rotated" (start 0 0) (end 4 -8) (angle 90) (layer "F.SilkS") (effects (font (size 1 1))) (border no))))`
	b, err := LoadBoard(memSource{"b.kicad_pcb": src}, "b.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	svg := renderBoardBuiltin(b, []string{"F.SilkS"}, false).SVG
	if !bytes.Contains(svg, []byte(`rotate(-180)`)) {
		t.Fatalf("local text-box angle does not compose with footprint:\n%s", svg)
	}
}

func TestBuiltinInvalidPadSize(t *testing.T) {
	b := marginBoard(t, "", "", `(solder_mask_margin 1)`)
	b.Node.Child("footprint").Child("pad").Child("size").List[1].Atom = "0"
	if bytes.Contains(renderBoardBuiltin(b, []string{"F.Mask"}, false).SVG, []byte("<path")) {
		t.Fatal("invalid pad size rendered")
	}
}
