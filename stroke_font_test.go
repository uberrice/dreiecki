package main

import (
	"math"
	"strings"
	"testing"
)

func TestStrokeFontGlyphs(t *testing.T) {
	// KiCad's A is I[MUWU RK[RFY[: bearings I/[ and two pen strokes.
	// At height 21 each encoded coordinate is one millimetre.
	if got := strokeLinePath("A", 0, 0, textStyle{size: 21}); got != "M4 -5L14 -5M2 1L9 -20L16 1" {
		t.Fatalf("A geometry: %s", got)
	}
	if got := textWidth("A A", 21); math.Abs(got-52) > 1e-9 {
		t.Errorf("advance including space = %g, want 52", got)
	}
	if textWidth("WWW", 1) <= textWidth("iii", 1) {
		t.Error("text measurement ignores glyph advances")
	}
	if got := wrapText("iii iii WWW WWW", 4, 1); got != "iii iii\nWWW\nWWW" {
		t.Errorf("font-metric wrapping: %q", got)
	}
	if got := textWidth("A\nAA", 21); math.Abs(got-36) > 1e-9 {
		t.Errorf("multiline width = %g, want 36", got)
	}
	for _, c := range []rune{'Ω', '中'} {
		if strokeGlyph(c) == strokeGlyph('?') || strokeLinePath(string(c), 0, 0, textStyle{size: 1}) == "" {
			t.Errorf("missing Unicode glyph %c", c)
		}
	}
	for _, c := range []rune{0, '\U0001f600'} {
		if strokeGlyph(c) != strokeGlyph('?') {
			t.Errorf("unsupported rune %U has no fallback", c)
		}
	}
}

func TestStrokeTextLayout(t *testing.T) {
	for _, tc := range []struct {
		hj   int
		path string
	}{
		{-1, "M4 -5L14 -5M2 1L9 -20L16 1"},
		{0, "M-5 -5L5 -5M-7 1L0 -20L7 1"},
		{1, "M-14 -5L-4 -5M-16 1L-9 -20L-2 1"},
	} {
		s := newSVG(100, 100)
		s.text(10, 20, "A", textStyle{size: 21, hj: tc.hj, vj: 1, angle: 90, mirror: true, color: "#123456"})
		svg := string(s.page(100, 100).SVG)
		for _, want := range []string{`translate(10 20) rotate(-90) scale(-1 1)`, `stroke="#123456"`, `d="` + tc.path + `"`} {
			if !strings.Contains(svg, want) {
				t.Errorf("justify %d missing %s in %s", tc.hj, want, svg)
			}
		}
		if strings.Contains(svg, "<text") || strings.Contains(svg, "font-family") {
			t.Error("SVG still depends on browser fonts")
		}
	}
}

func TestStrokeTextEffectsAndEscaping(t *testing.T) {
	n, err := ParseSexpr(`(text "A" (effects (font (size 2 3) (thickness 0.4) bold italic) (justify left top mirror)))`)
	if err != nil {
		t.Fatal(err)
	}
	st := effects(n, textStyle{color: "red"})
	if st.size != 2 || st.width != 3 || st.thickness != 0.4 || !st.bold || !st.italic || !st.mirror {
		t.Fatalf("font effects: %+v", st)
	}
	plain := strokeLinePath("A", 0, 0, textStyle{size: 2})
	if styled := strokeLinePath("A", 0, 0, st); styled == plain {
		t.Error("width and italic effects ignored")
	}
	s := newSVG(100, 100)
	s.text(0, 0, "<&\"\nΩ中", st)
	svg := string(s.page(100, 100).SVG)
	if !strings.Contains(svg, `stroke-width="0.4"`) || !strings.Contains(svg, "&lt;&amp;&quot;") || strings.Count(svg, "<path") != 2 {
		t.Fatalf("styled multiline text: %s", svg)
	}
	for _, tc := range []struct {
		bold bool
		want string
	}{{false, `stroke-width="1"`}, {true, `stroke-width="1.6"`}} {
		s := newSVG(100, 100)
		s.text(0, 0, "A", textStyle{size: 8, bold: tc.bold})
		if !strings.Contains(s.String(), tc.want) {
			t.Errorf("bold %t: %s", tc.bold, s.String())
		}
	}
}
