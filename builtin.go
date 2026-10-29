package main

// The built-in renderer draws schematics and boards as SVG without kicad-cli.
// It is less faithful than KiCad's own plotter (system fonts instead of the
// stroke font, the default drawing sheet only, simplified pin and label shapes),
// and simplified graphics can hide changes in the visual comparison. The
// structured comparison does not depend on this renderer.

import (
	"bytes"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// xform maps local coordinates to the page: (a*x + b*y + x0, c*x + d*y + y0).
type xform struct{ a, b, c, d, x0, y0 float64 }

var identity = xform{a: 1, d: 1}

func (t xform) pt(x, y float64) (float64, float64) { return t.a*x + t.b*y + t.x0, t.c*x + t.d*y + t.y0 }

// then applies o after t, ignoring o's translation.
func (t xform) then(o xform) xform {
	return xform{o.a*t.a + o.b*t.c, o.a*t.b + o.b*t.d, o.c*t.a + o.d*t.c, o.c*t.b + o.d*t.d,
		o.a*t.x0 + o.b*t.y0, o.c*t.x0 + o.d*t.y0}
}

func xy(n *Node) (float64, float64) { return num(n.Arg(0)), num(n.Arg(1)) }

func xyNode(x, y float64) *Node {
	return &Node{IsList: true, List: []*Node{{Atom: "xy"}, {Atom: fmtNum(x)}, {Atom: fmtNum(y)}}}
}

// justify is -1 (left) if cond holds, 1 (right) otherwise.
func justify(cond bool) int {
	if cond {
		return -1
	}
	return 1
}

// parallel runs f(0) ... f(n-1) on up to NumCPU goroutines and waits for them.
func parallel(n int, f func(i int)) {
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < min(n, runtime.NumCPU()); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				f(i)
			}
		}()
	}
	for i := 0; i < n; i++ {
		next <- i
	}
	close(next)
	wg.Wait()
}

// svgDoc collects the SVG of one page; coordinates are millimetres.
type svgDoc struct{ bytes.Buffer }

func newSVG(w, h float64) *svgDoc {
	s := &svgDoc{}
	fmt.Fprintf(s, `<svg xmlns="http://www.w3.org/2000/svg" width="%smm" height="%smm" viewBox="0 0 %s %s">`+"\n",
		ff(w), ff(h), ff(w), ff(h))
	s.WriteString(`<g fill="none" stroke-linecap="round" stroke-linejoin="round" font-family="sans-serif">` + "\n")
	return s
}

func (s *svgDoc) page(w, h float64) *RenderedPage {
	s.WriteString("</g>\n</svg>\n")
	return &RenderedPage{SVG: s.Bytes(), Width: w, Height: h}
}

// ff formats a coordinate compactly.
func ff(f float64) string {
	f = math.Round(f*1e4) / 1e4
	if f == 0 {
		return "0"
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

var xmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

// kicadEscapes are the {token} escapes of KiCad's UnescapeString.
var kicadEscapes = map[string]string{
	"slash": "/", "backslash": `\`, "colon": ":", "dblquote": `"`, "quote": "'", "lt": "<", "gt": ">",
	"bar": "|", "comma": ",", "tab": " ", "return": "\n", "space": " ", "dollar": "$", "brace": "{",
}

// kicadText removes KiCad's text markup: the overbar, superscript and subscript
// braces, and escapes such as {slash}. Both are read in one pass, so an escaped
// brace never opens or closes a group.
func kicadText(s string) string {
	var b strings.Builder
	depth := 0
	for i := 0; i < len(s); i++ {
		switch {
		case (s[i] == '~' || s[i] == '^' || s[i] == '_') && i+1 < len(s) && s[i+1] == '{':
			depth++
			i++
		case s[i] == '}' && depth > 0:
			depth--
		case s[i] == '{':
			if j := strings.IndexByte(s[i:], '}'); j > 0 {
				if r, ok := kicadEscapes[s[i+1:i+j]]; ok {
					b.WriteString(r)
					i += j
					continue
				}
			}
			b.WriteByte(s[i])
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

func (s *svgDoc) line(x1, y1, x2, y2 float64, color string, w float64) {
	fmt.Fprintf(s, `<path d="M%s %sL%s %s" stroke="%s" stroke-width="%s"/>`+"\n", ff(x1), ff(y1), ff(x2), ff(y2), color, ff(w))
}

// path draws d with an optional fill and a stroke if w > 0, or a hairline if
// there is no fill. An empty stroke colour strokes with the fill colour.
func (s *svgDoc) path(d, stroke string, w float64, fill string) {
	s.WriteString(`<path d="` + d + `"`)
	if fill != "" {
		s.WriteString(` fill="` + fill + `" fill-rule="evenodd"`)
	}
	if w > 0 || fill == "" {
		if stroke == "" {
			stroke = fill
		}
		fmt.Fprintf(s, ` stroke="%s" stroke-width="%s"`, stroke, ff(math.Max(w, 0.01)))
	}
	s.WriteString("/>\n")
}

func (s *svgDoc) circle(x, y, r float64, stroke string, w float64, fill string) {
	s.path(circlePath(x, y, r), stroke, w, fill)
}

func circlePath(x, y, r float64) string {
	return fmt.Sprintf("M%s %sa%s %s 0 1 0 %s 0a%s %s 0 1 0 %s 0Z", ff(x-r), ff(y), ff(r), ff(r), ff(2*r), ff(r), ff(r), ff(-2*r))
}

// rectPath is the rectangle with corners (x1, y1) and (x2, y2) through t.
func rectPath(x1, y1, x2, y2 float64, t xform) string {
	return ptsPath(&Node{IsList: true, List: []*Node{xyNode(x1, y1), xyNode(x2, y1), xyNode(x2, y2), xyNode(x1, y2)}}, t, true)
}

// bezierPath is the cubic Bézier of (pts (xy ...)) through t, or "" unless it has four points.
func bezierPath(pts *Node, t xform) string {
	var p []string
	for _, c := range pts.Children("xy") {
		x, y := t.pt(xy(c))
		p = append(p, ff(x)+" "+ff(y))
	}
	if len(p) != 4 {
		return ""
	}
	return "M" + p[0] + "C" + strings.Join(p[1:], " ")
}

// arcTo continues a path with the circular arc through (mx, my) to (ex, ey).
func arcTo(sx, sy, mx, my, ex, ey float64) string {
	// Circle through three points.
	d := 2 * (sx*(my-ey) + mx*(ey-sy) + ex*(sy-my))
	if math.Abs(d) < 1e-9 {
		return fmt.Sprintf("L%s %s", ff(ex), ff(ey))
	}
	s2, m2, e2 := sx*sx+sy*sy, mx*mx+my*my, ex*ex+ey*ey
	cx := (s2*(my-ey) + m2*(ey-sy) + e2*(sy-my)) / d
	cy := (s2*(ex-mx) + m2*(sx-ex) + e2*(mx-sx)) / d
	r := math.Hypot(sx-cx, sy-cy)
	cw := (mx-sx)*(ey-my)-(my-sy)*(ex-mx) > 0 // clockwise on screen, positive SVG angles
	a0, a1 := math.Atan2(sy-cy, sx-cx), math.Atan2(ey-cy, ex-cx)
	span := a1 - a0
	if !cw {
		span = -span
	}
	span = math.Mod(span+4*math.Pi, 2*math.Pi)
	large, sweep := 0, 0
	if span > math.Pi {
		large = 1
	}
	if cw {
		sweep = 1
	}
	if math.Hypot(ex-sx, ey-sy) < 1e-9 { // a full circle
		return fmt.Sprintf("A%s %s 0 1 %d %s %sA%s %s 0 1 %d %s %s", ff(r), ff(r), sweep, ff(2*cx-sx), ff(2*cy-sy), ff(r), ff(r), sweep, ff(ex), ff(ey))
	}
	return fmt.Sprintf("A%s %s 0 %d %d %s %s", ff(r), ff(r), large, sweep, ff(ex), ff(ey))
}

// textStyle describes a text in KiCad terms. Angles are counterclockwise degrees.
type textStyle struct {
	size   float64 // glyph height, mm
	color  string
	hj, vj int // -1 left/top, 0 centre, 1 right/bottom
	angle  float64
	mirror bool
	bold   bool
}

// text draws possibly multi-line text anchored at (x, y).
func (s *svgDoc) text(x, y float64, str string, st textStyle) {
	str = kicadText(str)
	if strings.TrimSpace(str) == "" || st.size <= 0 {
		return
	}
	lines := strings.Split(str, "\n")
	lh := st.size * 1.6
	// Baseline of the first line; a capital is about st.size tall.
	base := st.size
	switch st.vj {
	case 0:
		base = st.size/2 - float64(len(lines)-1)*lh/2
	case 1:
		base = -float64(len(lines)-1) * lh
	}
	anchor := [3]string{"start", "middle", "end"}[st.hj+1]
	tf := fmt.Sprintf("translate(%s %s)", ff(x), ff(y))
	if st.angle != 0 {
		tf += fmt.Sprintf(" rotate(%s)", ff(-st.angle))
	}
	if st.mirror {
		tf += " scale(-1 1)"
	}
	fmt.Fprintf(s, `<text transform="%s" font-size="%s" fill="%s" stroke="none" text-anchor="%s"`, tf, ff(st.size*1.4), st.color, anchor)
	if st.bold {
		s.WriteString(` font-weight="bold"`)
	}
	s.WriteString(">")
	for i, l := range lines {
		fmt.Fprintf(s, `<tspan x="0" y="%s">%s</tspan>`, ff(base+float64(i)*lh), xmlEscaper.Replace(l))
	}
	s.WriteString("</text>\n")
}

// textWidth estimates the width of a single line of text.
func textWidth(str string, size float64) float64 {
	return float64(len([]rune(kicadText(str)))) * size * 0.8
}

// effects reads the font size, justification and mirroring of an (effects ...) node.
func effects(n *Node, st textStyle) textStyle {
	e := n.Child("effects")
	if f := e.Child("font"); f != nil {
		if sz := f.Child("size"); sz != nil {
			st.size = num(sz.Arg(0))
		}
		st.bold = f.Child("bold") != nil && f.Child("bold").Arg(0) != "no" || hasAtom(f, "bold")
	}
	if j := e.Child("justify"); j != nil {
		for i := 0; j.Arg(i) != ""; i++ {
			switch j.Arg(i) {
			case "left":
				st.hj = -1
			case "right":
				st.hj = 1
			case "top":
				st.vj = -1
			case "bottom":
				st.vj = 1
			case "mirror":
				st.mirror = true
			}
		}
	}
	return st
}

// paperSize returns the page size of a schematic or board in mm.
func paperSize(n *Node) (float64, float64) {
	sizes := map[string][2]float64{
		"A5": {210, 148}, "A4": {297, 210}, "A3": {420, 297}, "A2": {594, 420}, "A1": {841, 594}, "A0": {1189, 841},
		"A": {279.4, 215.9}, "B": {431.8, 279.4}, "C": {558.8, 431.8}, "D": {863.6, 558.8}, "E": {1117.6, 863.6},
		"USLetter": {279.4, 215.9}, "USLegal": {355.6, 215.9}, "USLedger": {431.8, 279.4},
	}
	p := n.Child("paper")
	if p.Arg(0) == "User" && num(p.Arg(1)) > 0 && num(p.Arg(2)) > 0 {
		return num(p.Arg(1)), num(p.Arg(2))
	}
	sz, ok := sizes[p.Arg(0)]
	if !ok {
		sz = sizes["A4"]
	}
	if hasAtom(p, "portrait") {
		return sz[1], sz[0]
	}
	return sz[0], sz[1]
}

// drawFrame draws KiCad's default drawing sheet: a double border with grid
// references and the title block. Custom .kicad_wks files are not supported.
func (s *svgDoc) drawFrame(w, h float64, root *Node, color, sheet, file, page string, pages int) {
	const m, gap, lw = 10.0, 2.0, 0.15
	s.path(fmt.Sprintf("M%s %sH%sV%sH%sZM%s %sH%sV%sH%sZ", ff(m), ff(m), ff(w-m), ff(h-m), ff(m),
		ff(m+gap), ff(m+gap), ff(w-m-gap), ff(h-m-gap), ff(m+gap)), color, lw, "")
	st := textStyle{size: 1.3, color: color}
	cols, rows := int(math.Ceil((w-2*m)/50)), int(math.Ceil((h-2*m)/50))
	for i := 0; i < cols; i++ {
		x := m + float64(i)*50
		if i > 0 {
			s.line(x, m, x, m+gap, color, lw)
			s.line(x, h-m, x, h-m-gap, color, lw)
		}
		cx := (x + math.Min(x+50, w-m)) / 2
		s.text(cx, m+gap/2, strconv.Itoa(i+1), st)
		s.text(cx, h-m-gap/2, strconv.Itoa(i+1), st)
	}
	for i := 0; i < rows; i++ {
		y := m + float64(i)*50
		if i > 0 {
			s.line(m, y, m+gap, y, color, lw)
			s.line(w-m, y, w-m-gap, y, color, lw)
		}
		cy := (y + math.Min(y+50, h-m)) / 2
		s.text(m+gap/2, cy, string(rune('A'+i%26)), st)
		s.text(w-m-gap/2, cy, string(rune('A'+i%26)), st)
	}

	// Title block in the bottom right corner.
	tb := root.Child("title_block")
	field := func(k string) string { return tb.Child(k).Arg(0) }
	x0, x1, y1 := w-m-gap-110, w-m-gap, h-m-gap
	row := func(dy float64) float64 { return y1 - dy }
	s.path(fmt.Sprintf("M%s %sV%sH%s", ff(x0), ff(y1), ff(row(32)), ff(x1)), color, lw, "")
	for _, dy := range []float64{4, 8, 12, 16, 20} {
		s.line(x0, row(dy), x1, row(dy), color, lw)
	}
	s.line(x0+88, row(4), x0+88, row(8), color, lw)
	s.line(x0+75, row(8), x0+75, row(16), color, lw)
	left := textStyle{size: 1.5, color: color, hj: -1}
	s.text(x0+1, row(2), "KiCad E.D.A.", left)
	s.text(x0+89, row(2), "Id: "+page+"/"+strconv.Itoa(pages), left)
	s.text(x0+1, row(6), "Size: "+root.Child("paper").Arg(0), left)
	s.text(x0+20, row(6), "Date: "+field("date"), left)
	s.text(x0+89, row(6), "Rev: "+field("rev"), left)
	s.text(x0+1, row(10), "File: "+file, left)
	s.text(x0+1, row(14), "Sheet: "+sheet, left)
	big := left
	big.size, big.bold = 2, true
	s.text(x0+1, row(18), "Title: "+field("title"), big)
	s.text(x0+1, row(22), field("company"), left)
	for i, c := range tb.Children("comment") {
		if i < 4 {
			s.text(x0+1, row(24.5+float64(i)*2), c.Arg(1), textStyle{size: 1.2, color: color, hj: -1})
		}
	}
}
