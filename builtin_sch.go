package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Colours of KiCad's default schematic theme.
const (
	colWire     = "#009600"
	colBus      = "#0000c8"
	colBody     = "#840000"
	colBodyFill = "#ffffc2"
	colPinName  = "#006464"
	colPinNum   = "#a90000"
	colRef      = "#006464"
	colField    = "#840084"
	colNote     = "#000084"
	colLabel    = "#000000"
	colGlobal   = "#840000"
	colHier     = "#846400"
	colSheet    = "#840084"
	colFrame    = "#840000"
	schLine     = 0.1524 // default line width, 6 mil
)

// renderSheetBuiltin draws one sheet instance of p.
func renderSheetBuiltin(p *Project, inst *SheetInst, frame bool) *RenderedPage {
	sch := p.Parsed[inst.File]
	w, h := paperSize(sch)
	s := newSVG(w, h)
	if frame {
		s.drawFrame(w, h, sch, colFrame, inst.DisplayName(), inst.File[strings.LastIndex(inst.File, "/")+1:], inst.Page, len(p.Sheets))
	}
	libs := map[string]*Node{}
	for _, l := range sch.Child("lib_symbols").Children("symbol") {
		libs[l.Arg(0)] = l
	}
	for _, n := range sch.List {
		switch n.Head() {
		case "wire", "bus":
			col, lw := colWire, schLine
			if n.Head() == "bus" {
				col, lw = colBus, 2*schLine
			}
			col, lw = strokeOf(n, col, lw)
			s.path(ptsPath(n.Child("pts"), identity, false), col, lw, "")
		case "bus_entry":
			x, y := xy(n.Child("at"))
			dx, dy := xy(n.Child("size"))
			s.line(x, y, x+dx, y+dy, colBus, schLine)
		case "junction":
			x, y := xy(n.Child("at"))
			d := num(n.Child("diameter").Arg(0))
			if d <= 0 {
				d = 0.9144
			}
			s.circle(x, y, d/2, "", 0, colorOf(n.Child("color"), colWire))
		case "no_connect":
			x, y := xy(n.Child("at"))
			const r = 0.6096
			s.path(fmt.Sprintf("M%s %sL%s %sM%s %sL%s %s", ff(x-r), ff(y-r), ff(x+r), ff(y+r), ff(x-r), ff(y+r), ff(x+r), ff(y-r)), colBus, schLine, "")
		case "polyline", "rectangle", "circle", "arc", "bezier":
			drawGraphic(s, n, identity, colNote)
		case "text":
			st := effects(n, textStyle{size: 1.27, color: colNote})
			x, y, a := atOf(n)
			st.angle = math.Mod(a, 180)
			s.text(x, y, n.Arg(0), st)
		case "text_box":
			x, y, _ := atOf(n)
			bw, bh := xy(n.Child("size"))
			col, lw := strokeOf(n, colNote, schLine)
			s.path(fmt.Sprintf("M%s %sh%sv%sh%sZ", ff(x), ff(y), ff(bw), ff(bh), ff(-bw)), col, lw, fillOf(n, col))
			st := effects(n, textStyle{size: 1.27, color: colNote, hj: -1, vj: -1})
			s.text(x+0.5, y+0.5, n.Arg(0), st)
		case "label", "global_label", "hierarchical_label", "directive_label", "netclass_flag":
			drawLabel(s, n)
		case "symbol":
			drawSymbol(s, n, libs, inst)
		case "sheet":
			drawSheet(s, n)
		}
	}
	return s.page(w, h)
}

func atOf(n *Node) (x, y, angle float64) {
	at := n.Child("at")
	return num(at.Arg(0)), num(at.Arg(1)), num(at.Arg(2))
}

// strokeOf returns the stroke colour and width of n, with defaults for unset values.
func strokeOf(n *Node, col string, w float64) (string, float64) {
	st := n.Child("stroke")
	if sw := num(st.Child("width").Arg(0)); sw > 0 {
		w = sw
	} else if sw := num(n.Child("width").Arg(0)); sw > 0 {
		w = sw
	}
	return colorOf(st.Child("color"), col), w
}

// colorOf reads (color r g b a); a fully transparent colour means the default.
func colorOf(c *Node, def string) string {
	if c == nil || num(c.Arg(3)) == 0 {
		return def
	}
	return fmt.Sprintf("rgb(%d,%d,%d)", int(num(c.Arg(0))), int(num(c.Arg(1))), int(num(c.Arg(2))))
}

// fillOf returns the fill colour of a schematic shape, or "" for none.
func fillOf(n *Node, stroke string) string {
	f := n.Child("fill")
	switch f.Child("type").Arg(0) {
	case "outline":
		return stroke
	case "background":
		return colBodyFill
	case "color":
		return colorOf(f.Child("color"), "")
	}
	return colorOf(f.Child("color"), "") // sheets and text boxes: (fill (color ...))
}

// ptsPath converts (pts (xy ...) (arc ...) ...) to path data.
func ptsPath(pts *Node, t xform, closed bool) string {
	var b strings.Builder
	var lx, ly float64
	first := true
	moveOrLine := func(x, y float64) {
		cmd := "L"
		if first {
			cmd, first = "M", false
		}
		fmt.Fprintf(&b, "%s%s %s", cmd, ff(x), ff(y))
		lx, ly = x, y
	}
	for _, c := range orEmpty(pts).List {
		switch c.Head() {
		case "xy":
			moveOrLine(t.pt(xy(c)))
		case "arc":
			sx, sy := t.pt(xy(c.Child("start")))
			mx, my := t.pt(xy(c.Child("mid")))
			ex, ey := t.pt(xy(c.Child("end")))
			if first || math.Hypot(sx-lx, sy-ly) > 1e-6 {
				moveOrLine(sx, sy)
			}
			b.WriteString(arcTo(sx, sy, mx, my, ex, ey))
			lx, ly = ex, ey
		}
	}
	if closed && !first {
		b.WriteString("Z")
	}
	return b.String()
}

// drawGraphic draws a schematic or symbol shape through t.
func drawGraphic(s *svgDoc, n *Node, t xform, col string) {
	col, lw := strokeOf(n, col, schLine)
	if n.Child("stroke").Child("type").Arg(0) == "none" {
		lw = 0
	}
	fill := fillOf(n, col)
	var d string
	switch n.Head() {
	case "polyline":
		d = ptsPath(n.Child("pts"), t, false)
	case "rectangle":
		x1, y1 := xy(n.Child("start"))
		x2, y2 := xy(n.Child("end"))
		d = rectPath(x1, y1, x2, y2, t)
	case "circle":
		cx, cy := t.pt(xy(n.Child("center")))
		r := num(n.Child("radius").Arg(0))
		d = circlePath(cx, cy, r)
	case "arc":
		sx, sy := t.pt(xy(n.Child("start")))
		mx, my := t.pt(xy(n.Child("mid")))
		ex, ey := t.pt(xy(n.Child("end")))
		d = fmt.Sprintf("M%s %s", ff(sx), ff(sy)) + arcTo(sx, sy, mx, my, ex, ey)
	case "bezier":
		d = bezierPath(n.Child("pts"), t)
	}
	if d != "" {
		s.path(d, col, lw, fill)
	}
}

// orient makes text with the given angle and justification, as seen through the
// linear part of t, upright again. KiCad draws schematic text at 0 or 90 degrees only.
func orient(st textStyle, t xform) textStyle {
	ux, uy, vx, vy := 1.0, 0.0, 0.0, 1.0 // reading and downward direction
	if math.Abs(math.Mod(st.angle, 180)-90) < 1 {
		ux, uy, vx, vy = 0, -1, 1, 0
	}
	ux, uy = t.a*ux+t.b*uy, t.c*ux+t.d*uy
	vx, vy = t.a*vx+t.b*vy, t.c*vx+t.d*vy
	if math.Abs(ux) > 0.5 {
		st.angle = 0
		if ux < 0 {
			st.hj = -st.hj
		}
		if vy < 0 {
			st.vj = -st.vj
		}
	} else {
		st.angle = 90
		if uy > 0 {
			st.hj = -st.hj
		}
		if vx < 0 {
			st.vj = -st.vj
		}
	}
	return st
}

// symbolXform returns the library-to-page transform of a placed symbol and its
// screen-space part without the library's upward Y axis.
func symbolXform(n *Node) (lib, screen xform) {
	x, y, a := atOf(n)
	screen = identity
	for i := 0; i < int(math.Round(math.Mod(a+360, 360)/90))%4; i++ {
		screen = screen.then(xform{a: 0, b: 1, c: -1, d: 0}) // 90° counterclockwise
	}
	switch n.Child("mirror").Arg(0) {
	case "x":
		screen = screen.then(xform{a: 1, d: -1})
	case "y":
		screen = screen.then(xform{a: -1, d: 1})
	}
	lib = xform{a: 1, d: -1}.then(screen)
	lib.x0, lib.y0 = x, y
	return lib, screen
}

// unitStyle parses the unit and body style from a sub-symbol name such as "R_1_1".
func unitStyle(name string) (int, int) {
	parts := strings.Split(name, "_")
	if len(parts) < 3 {
		return 0, 0
	}
	u, _ := strconv.Atoi(parts[len(parts)-2])
	st, _ := strconv.Atoi(parts[len(parts)-1])
	return u, st
}

func drawSymbol(s *svgDoc, n *Node, libs map[string]*Node, inst *SheetInst) {
	c := extractComponent(n, inst, nil)
	name := c.LibID
	if ln := n.Child("lib_name"); ln != nil {
		name = ln.Arg(0)
	}
	lib := libs[name]
	t, screen := symbolXform(n)
	unit, _ := strconv.Atoi(c.Unit)
	style := 1
	for _, h := range []string{"body_style", "convert"} {
		if bs := n.Child(h); bs != nil {
			style, _ = strconv.Atoi(bs.Arg(0))
		}
	}
	units := map[int]bool{}
	var pins []*Node
	var body bounds
	for _, sub := range lib.Children("symbol") {
		u, st := unitStyle(sub.Arg(0))
		units[u] = u > 0
		if u != 0 && u != unit || st != 0 && st != style {
			continue
		}
		for _, g := range sub.List {
			switch g.Head() {
			case "polyline", "rectangle", "circle", "arc", "bezier":
				drawGraphic(s, g, t, colBody)
				body.addLocal(g)
				if g.Head() == "circle" {
					cx, cy := xy(g.Child("center"))
					r := num(g.Child("radius").Arg(0))
					body.add(cx-r, cy-r)
					body.add(cx+r, cy+r)
				}
			case "text":
				x, y := t.pt(xy(g.Child("at")))
				st := effects(g, textStyle{size: 1.27, color: colBody})
				st.angle = num(g.Child("at").Arg(2))
				s.text(x, y, g.Arg(0), orient(st, screen))
			case "pin":
				pins = append(pins, g)
			}
		}
	}
	pinNames, pinNums := lib.Child("pin_names"), lib.Child("pin_numbers")
	offset := 0.508
	if o := pinNames.Child("offset"); o != nil {
		offset = num(o.Arg(0))
	}
	for _, p := range pins {
		drawPin(s, p, t, offset, !fieldHidden(orEmpty(pinNames)), !fieldHidden(orEmpty(pinNums)))
	}
	multi := 0
	for _, ok := range units {
		if ok {
			multi++
		}
	}
	for _, pr := range n.Children("property") {
		if fieldHidden(pr) {
			continue
		}
		val, col := pr.Arg(1), colField
		switch pr.Arg(0) {
		case "Reference":
			val, col = c.Ref, colRef
			if multi > 1 && unit > 0 {
				val += string(rune('A' + (unit-1)%26))
			}
		case "Value":
			col = colRef
		}
		if sn := pr.Child("show_name"); sn != nil && sn.Arg(0) == "yes" {
			val = pr.Arg(0) + ": " + val
		}
		x, y, a := atOf(pr)
		st := effects(pr, textStyle{size: 1.27, color: col, angle: a})
		s.text(x, y, val, orient(st, screen))
	}
	if n.Child("dnp").Arg(0) == "yes" {
		// The marker spans the selected body, including common graphics, and follows
		// the symbol transform. A fallback keeps pin-only symbols visible as DNP.
		x1, y1, x2, y2 := -1.27, -1.27, 1.27, 1.27
		for i, x := range body.xs {
			y := body.ys[i]
			if i == 0 {
				x1, y1, x2, y2 = x, y, x, y
			} else {
				x1 = math.Min(x1, x)
				y1 = math.Min(y1, y)
				x2 = math.Max(x2, x)
				y2 = math.Max(y2, y)
			}
		}
		const pad = 0.5
		ax, ay := t.pt(x1-pad, y1-pad)
		bx, by := t.pt(x2+pad, y2+pad)
		cx, cy := t.pt(x1-pad, y2+pad)
		dx, dy := t.pt(x2+pad, y1-pad)
		s.WriteString(`<g data-dnp="yes">`)
		s.line(ax, ay, bx, by, "#ff0000", 2*schLine)
		s.line(cx, cy, dx, dy, "#ff0000", 2*schLine)
		s.WriteString("</g>\n")
	}

}

// drawPin draws a library pin, its name and number through t.
func drawPin(s *svgDoc, p *Node, t xform, offset float64, names, numbers bool) {
	if fieldHidden(p) {
		return
	}
	x, y, a := atOf(p)
	l := num(p.Child("length").Arg(0))
	sa, ca := math.Sincos(a * math.Pi / 180)
	x0, y0 := t.pt(x, y)
	x1, y1 := t.pt(x+l*ca, y+l*sa)
	dx, dy := 0.0, 0.0
	if l > 0 {
		dx, dy = (x1-x0)/l, (y1-y0)/l
	}
	ex, ey := x1, y1 // where the line ends
	switch p.Arg(1) {
	case "inverted", "inverted_clock":
		const r = 0.381
		s.circle(x1-dx*r, y1-dy*r, r, colBody, schLine, "")
		ex, ey = x1-2*r*dx, y1-2*r*dy
	}
	switch p.Arg(1) {
	case "clock", "inverted_clock", "clock_low", "falling_edge":
		const c = 0.635
		s.path(fmt.Sprintf("M%s %sL%s %sL%s %s", ff(x1-dy*c), ff(y1+dx*c), ff(x1+dx*c), ff(y1+dy*c), ff(x1+dy*c), ff(y1-dx*c)), colBody, schLine, "")
	}
	s.line(x0, y0, ex, ey, colBody, schLine)

	vertical := math.Abs(dy) > 0.5
	text := func(px, py float64, str string, st textStyle) {
		if str == "" || str == "~" {
			return
		}
		if vertical {
			st.angle = 90
		}
		s.text(px, py, str, st)
	}
	name := effects(p.Child("name"), textStyle{size: 1.27, color: colPinName})
	number := effects(p.Child("number"), textStyle{size: 1.27, color: colPinNum, vj: 1})
	// Pointing right or up in the reading direction of the text.
	forward := dx > 0.5 || dy < -0.5
	mx, my := (x0+x1)/2, (y0+y1)/2
	// Above the pin: up for horizontal text, left for vertical text.
	ax, ay := 0.0, -0.3
	if vertical {
		ax, ay = -0.3, 0
	}
	if names && offset > 0 {
		name.hj = justify(forward)
		text(x1+dx*offset, y1+dy*offset, p.Child("name").Arg(0), name)
	} else if names {
		name.vj = 1
		text(mx+ax, my+ay, p.Child("name").Arg(0), name)
		number.vj = -1
		ax, ay = -ax, -ay
	}
	if numbers {
		text(mx+ax, my+ay, p.Child("number").Arg(0), number)
	}
}

func drawLabel(s *svgDoc, n *Node) {
	x, y, a := atOf(n)
	sa, ca := math.Sincos(a * math.Pi / 180)
	dx, dy := ca, -sa // away from the connection point
	st := effects(n, textStyle{size: 1.27, color: colLabel})
	st.angle = math.Mod(a, 180)
	size := st.size
	shape := func(pts [][2]float64, col string) {
		var b strings.Builder
		for i, p := range pts {
			cmd := "L"
			if i == 0 {
				cmd = "M"
			}
			fmt.Fprintf(&b, "%s%s %s", cmd, ff(x+p[0]*dx-p[1]*dy), ff(y+p[0]*dy+p[1]*dx))
		}
		b.WriteString("Z")
		s.path(b.String(), col, schLine, "")
	}
	switch n.Head() {
	case "label":
		ox, oy := 0.0, -0.25
		if st.angle == 90 {
			ox, oy = -0.25, 0
		}
		s.text(x+ox, y+oy, n.Arg(0), st)
	case "global_label":
		h := size * 1.6
		l := textWidth(n.Arg(0), size) + h*1.2
		var pts [][2]float64
		switch n.Child("shape").Arg(0) {
		case "input":
			pts = [][2]float64{{0, 0}, {h / 2, -h / 2}, {l, -h / 2}, {l, h / 2}, {h / 2, h / 2}}
		case "output":
			pts = [][2]float64{{0, -h / 2}, {l - h/2, -h / 2}, {l, 0}, {l - h/2, h / 2}, {0, h / 2}}
		case "bidirectional", "tri_state":
			pts = [][2]float64{{0, 0}, {h / 2, -h / 2}, {l - h/2, -h / 2}, {l, 0}, {l - h/2, h / 2}, {h / 2, h / 2}}
		default:
			pts = [][2]float64{{0, -h / 2}, {l, -h / 2}, {l, h / 2}, {0, h / 2}}
		}
		st.color, st.hj, st.vj = colGlobal, 0, 0
		shape(pts, colGlobal)
		s.text(x+dx*l/2, y+dy*l/2, n.Arg(0), st)
	case "hierarchical_label":
		h := size
		var pts [][2]float64
		switch n.Child("shape").Arg(0) {
		case "input":
			pts = [][2]float64{{0, 0}, {h / 2, -h / 2}, {h, -h / 2}, {h, h / 2}, {h / 2, h / 2}}
		case "output":
			pts = [][2]float64{{0, -h / 2}, {h / 2, -h / 2}, {h, 0}, {h / 2, h / 2}, {0, h / 2}}
		case "bidirectional", "tri_state":
			pts = [][2]float64{{0, 0}, {h / 2, -h / 2}, {h, 0}, {h / 2, h / 2}}
		default:
			pts = [][2]float64{{0, -h / 2}, {h, -h / 2}, {h, h / 2}, {0, h / 2}}
		}
		st.color, st.vj = colHier, 0
		st.hj = justify(dx > 0.5 || dy < -0.5)
		shape(pts, colHier)
		s.text(x+dx*h*1.3, y+dy*h*1.3, n.Arg(0), st)
	default: // directive labels: a stem with a circle
		l := num(n.Child("length").Arg(0))
		if l <= 0 {
			l = 2.54
		}
		s.line(x, y, x+dx*l, y+dy*l, colGlobal, schLine)
		s.circle(x+dx*(l+0.5), y+dy*(l+0.5), 0.5, colGlobal, schLine, "")
	}
	for _, pr := range n.Children("property") {
		if !fieldHidden(pr) && pr.Arg(1) != "" {
			px, py, pa := atOf(pr)
			st := effects(pr, textStyle{size: 1.27, color: colField, angle: math.Mod(pa, 180)})
			s.text(px, py, pr.Arg(1), st)
		}
	}
}

func drawSheet(s *svgDoc, n *Node) {
	x, y, _ := atOf(n)
	w, h := xy(n.Child("size"))
	col, lw := strokeOf(n, colSheet, schLine)
	s.path(fmt.Sprintf("M%s %sh%sv%sh%sZ", ff(x), ff(y), ff(w), ff(h), ff(-w)), col, lw, fillOf(n, col))
	for _, pr := range n.Children("property") {
		if fieldHidden(pr) {
			continue
		}
		val, c := pr.Arg(1), colField
		switch pr.Arg(0) {
		case "Sheetname", "Sheet name":
			c = colRef
		case "Sheetfile", "Sheet file":
			val, c = "File: "+val, colBody
		}
		px, py, pa := atOf(pr)
		s.text(px, py, val, effects(pr, textStyle{size: 1.27, color: c, angle: math.Mod(pa, 180)}))
	}
	for _, p := range n.Children("pin") {
		px, py, pa := atOf(p)
		st := effects(p, textStyle{size: 1.27, color: colBody})
		sa, ca := math.Sincos(pa * math.Pi / 180)
		in := st.size * 0.5 // the text sits inside the sheet, opposite to the pin direction
		st.angle = math.Mod(pa, 180)
		s.text(px-ca*in, py+sa*in, p.Arg(0), st)
	}
}
