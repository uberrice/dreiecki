package main

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
)

// Colours of KiCad's default board theme.
var pcbColors = map[string]string{
	"F.Cu": "#c83434", "B.Cu": "#4d7fc4", "In1.Cu": "#7fc87f", "In2.Cu": "#ce7d2c", "In3.Cu": "#4fcbcb",
	"In4.Cu": "#db628b", "In5.Cu": "#a7a5c6", "In6.Cu": "#28cccc",
	"F.SilkS": "#f2eda1", "B.SilkS": "#e8b2a7", "F.Mask": "#d864ff", "B.Mask": "#02ffee",
	"F.Paste": "#b5b5b5", "B.Paste": "#00c2c2", "F.Adhes": "#a9549e", "B.Adhes": "#3a3aff",
	"F.Fab": "#afafaf", "B.Fab": "#585d84", "F.CrtYd": "#ff26e2", "B.CrtYd": "#26e9ff",
	"Edge.Cuts": "#d0d2cd", "Margin": "#ff26e2", "Dwgs.User": "#c2c2c2", "Cmts.User": "#5994dc",
	"Eco1.User": "#b4dbd2", "Eco2.User": "#d8c852",
}

const colPcbFrame = "#a0a0a0"

func pcbColor(l string) string {
	if c, ok := pcbColors[l]; ok {
		return c
	}
	if strings.HasSuffix(l, ".Cu") {
		return "#c8a050"
	}
	return "#c2c2c2"
}

// renderBoardBuiltin draws the given layers of b into one SVG, in order.
func renderBoardBuiltin(b *Board, layers []string, frame bool) *RenderedPage {
	w, h := paperSize(b.Node)
	s := newSVG(w, h)
	margins := boardPadMargins(b)
	for _, l := range layers {
		r := &boardLayer{Board: b, s: s, layer: l, color: pcbColor(l), copper: strings.HasSuffix(l, ".Cu"), margins: margins}
		fmt.Fprintf(s, "<g data-layer=\"%s\">\n", xmlEscaper.Replace(l))
		// Zone fills first, so tracks and pads stay visible on top of them.
		for _, n := range b.Node.List {
			switch n.Head() {
			case "zone":
				r.zone(n)
			case "footprint", "module":
				for _, z := range n.Children("zone") {
					r.zone(z)
				}
			}
		}
		for _, n := range b.Node.List {
			r.item(n, identity, n)
		}
		s.WriteString("</g>\n")
	}
	if frame {
		s.drawFrame(w, h, b.Node, colPcbFrame, "/", b.Name+".kicad_pcb", "1", 1)
	}
	return s.page(w, h)
}

type boardLayer struct {
	*Board
	s       *svgDoc
	layer   string
	color   string
	copper  bool
	margins padMargins
}

func (r *boardLayer) on(n *Node) bool { return indexOf(r.nodeLayers(n), r.layer) >= 0 }

func (r *boardLayer) zone(n *Node) {
	for _, fp := range n.Children("filled_polygon") {
		if fp.Child("layer").Arg(0) == r.layer {
			r.s.path(ptsPath(fp.Child("pts"), identity, true), "", 0, r.color)
		}
	}
}

// fpXform places footprint-local coordinates on the board.
func fpXform(n *Node) (xform, float64) {
	x, y, a := atOf(n)
	sa, ca := math.Sincos(a * math.Pi / 180)
	return xform{a: ca, b: sa, c: -sa, d: ca, x0: x, y0: y}, a
}

// item draws a board-level object, or a footprint child through the footprint transform t.
func (r *boardLayer) item(n *Node, t xform, fp *Node) {
	h := n.Head()
	switch h {
	case "footprint", "module":
		ft, _ := fpXform(n)
		for _, c := range n.List[1:] {
			if c.IsList && c.Head() != "zone" {
				r.item(c, ft, n)
			}
		}
	case "segment":
		if r.on(n) {
			x1, y1 := xy(n.Child("start"))
			x2, y2 := xy(n.Child("end"))
			r.s.line(x1, y1, x2, y2, r.color, num(n.Child("width").Arg(0)))
		}
	case "arc", "gr_arc", "fp_arc", "gr_line", "fp_line", "gr_rect", "fp_rect", "gr_circle", "fp_circle",
		"gr_poly", "fp_poly", "gr_curve", "fp_curve":
		if r.on(n) {
			r.shape(n, t)
		}
	case "via":
		if r.copper && r.on(n) {
			x, y := xy(n.Child("at"))
			d := circlePath(x, y, num(n.Child("size").Arg(0))/2) + circlePath(x, y, num(n.Child("drill").Arg(0))/2)
			r.s.path(d, "", 0, r.color)
		}
	case "pad":
		if r.on(n) {
			r.pad(n, t, fp)
		}
	case "gr_text", "fp_text", "property":
		if r.on(n) && !fieldHidden(n) && (h != "property" || n.Child("layer") != nil) {
			txt := n.Arg(0)
			if h == "fp_text" || h == "property" {
				txt = n.Arg(1)
			}
			if fp != n { // board-level text keeps its variables
				txt = strings.NewReplacer("${REFERENCE}", propValue(fp, "Reference"), "${VALUE}", propValue(fp, "Value")).Replace(txt)
			}
			if fp != n && txt == "" && h == "fp_text" {
				return
			}
			r.text(n, t, txt)
		}
	case "gr_text_box", "fp_text_box":
		if r.on(n) && !fieldHidden(n) {
			r.textBox(n, t, fp)
		}
	case "dimension":
		if gt := n.Child("gr_text"); gt != nil && r.on(n) {
			r.text(gt, identity, gt.Arg(0))
		}
	}
}

// shape draws a line, rectangle, circle, arc, polygon or curve through t.
func (r *boardLayer) shape(n *Node, t xform) {
	h := strings.TrimPrefix(strings.TrimPrefix(n.Head(), "gr_"), "fp_") // a track arc is just "arc"
	w := num(n.Child("width").Arg(0))
	if sw := n.Child("stroke").Child("width"); sw != nil {
		w = num(sw.Arg(0))
	}
	fill := ""
	if f := n.Child("fill"); f != nil && (f.Arg(0) == "yes" || f.Arg(0) == "solid" || f.Child("type").Arg(0) == "solid") {
		fill = r.color
	}
	pt := func(k string) (float64, float64) { return t.pt(xy(n.Child(k))) }
	var d string
	switch h {
	case "line":
		x1, y1 := pt("start")
		x2, y2 := pt("end")
		d = fmt.Sprintf("M%s %sL%s %s", ff(x1), ff(y1), ff(x2), ff(y2))
	case "rect":
		x1, y1 := xy(n.Child("start"))
		x2, y2 := xy(n.Child("end"))
		d = rectPath(x1, y1, x2, y2, t)
	case "circle":
		cx, cy := pt("center")
		ex, ey := pt("end")
		d = circlePath(cx, cy, math.Hypot(ex-cx, ey-cy))
	case "arc":
		sx, sy := pt("start")
		ex, ey := pt("end")
		if n.Child("mid") == nil {
			return // KiCad 5 arcs by centre and angle are not drawn
		}
		mx, my := pt("mid")
		d = fmt.Sprintf("M%s %s", ff(sx), ff(sy)) + arcTo(sx, sy, mx, my, ex, ey)
	case "poly":
		d = ptsPath(n.Child("pts"), t, true)
	case "curve":
		d = bezierPath(n.Child("pts"), t)
	}
	if d == "" {
		return
	}
	if h == "line" || h == "arc" {
		fill = ""
	}
	r.s.path(d, r.color, w, fill)
}

// pad draws a pad in its own frame; copper layers get the drill hole cut out.
func (r *boardLayer) pad(n *Node, t xform, fp *Node) {
	px, py, a := atOf(n)
	x, y := t.pt(px, py)
	w, h := xy(n.Child("size"))
	if w <= 0 || h <= 0 {
		return
	}
	ow, oh := w, h
	mx, my := r.padMargin(n, fp, w, h)
	w, h = w+2*mx, h+2*my
	if w <= 0 || h <= 0 {
		return
	}
	var d string
	switch n.Arg(2) {
	case "circle":
		d = circlePath(0, 0, w/2)
	case "oval":
		d = roundRect(w, h, math.Min(w, h)/2)
	case "roundrect":
		d = roundRect(w, h, num(n.Child("roundrect_rratio").Arg(0))*math.Min(ow, oh)+math.Min(mx, my))
	case "custom":
		if n.Child("options").Child("anchor").Arg(0) == "circle" {
			d = circlePath(0, 0, w/2)
		} else {
			d = roundRect(w, h, 0)
		}
	default: // rect, trapezoid and chamfered rectangles are drawn as rectangles
		d = roundRect(w, h, 0)
	}
	if n.Arg(1) == "np_thru_hole" && r.copper {
		d = "" // no copper around a non-plated hole
	}
	if dr := n.Child("drill"); dr != nil && r.copper && d != "" {
		ox, oy := xy(dr.Child("offset"))
		dw, dh := num(dr.Arg(0)), num(dr.Arg(0))
		if dr.Arg(0) == "oval" {
			dw, dh = num(dr.Arg(1)), num(dr.Arg(2))
			if dh == 0 {
				dh = dw
			}
		}
		if dw > 0 {
			d += roundRectAt(ox, oy, dw, dh, math.Min(dw, dh)/2)
		}
	}
	fmt.Fprintf(r.s, `<g transform="translate(%s %s) rotate(%s)">`, ff(x), ff(y), ff(-a))
	if d != "" {
		r.s.path(d, "", 0, r.color)
	}
	if n.Arg(2) == "custom" {
		// Custom outlines use an approximate scaled margin.
		fmt.Fprintf(r.s, `<g transform="scale(%s %s)">`, ff(w/ow), ff(h/oh))
		for _, g := range orEmpty(n.Child("primitives")).List {
			if g.IsList && strings.HasPrefix(g.Head(), "gr_") {
				r.shape(g, identity)
			}
		}
		r.s.WriteString("</g>\n")
	}
	r.s.WriteString("</g>\n")
}

func roundRect(w, h, rad float64) string { return roundRectAt(0, 0, w, h, rad) }

// roundRectAt is a w×h rectangle centred at (cx, cy) with corner radius rad.
func roundRectAt(cx, cy, w, h, rad float64) string {
	rad = math.Max(0, math.Min(rad, math.Min(w, h)/2))
	x, y := cx-w/2, cy-h/2
	if rad == 0 {
		return fmt.Sprintf("M%s %sh%sv%sh%sZ", ff(x), ff(y), ff(w), ff(h), ff(-w))
	}
	arc := func(dx, dy float64) string {
		return fmt.Sprintf("a%s %s 0 0 1 %s %s", ff(rad), ff(rad), ff(dx), ff(dy))
	}
	return fmt.Sprintf("M%s %sh%s", ff(x+rad), ff(y), ff(w-2*rad)) + arc(rad, rad) +
		fmt.Sprintf("v%s", ff(h-2*rad)) + arc(-rad, rad) + fmt.Sprintf("h%s", ff(2*rad-w)) + arc(-rad, -rad) +
		fmt.Sprintf("v%s", ff(2*rad-h)) + arc(rad, -rad) + "Z"
}

// text draws board or footprint text. Angles in the file are absolute; KiCad keeps text readable.
func (r *boardLayer) text(n *Node, t xform, txt string) {
	px, py, a := atOf(n)
	x, y := t.pt(px, py)
	st := effects(n, textStyle{size: 1, color: r.color})
	a = math.Mod(math.Mod(a, 360)+360, 360)
	if a > 90 && a <= 270 {
		a -= 180
		st.hj, st.vj = -st.hj, -st.vj
	}
	st.angle = a
	r.s.text(x, y, txt, st)
}

// renderPCBBuiltin renders the pages like RenderPCB does with kicad-cli, each plot on its own goroutine.
func renderPCBBuiltin(b *Board, pages []pcbPage, opt RenderOptions) map[string]*RenderedPage {
	type plot struct {
		layers []string
		frame  bool
		out    *RenderedPage
	}
	var plots []*plot
	rendered := map[string]*RenderedPage{}
	single := map[string]*plot{}
	stacks := map[string][]*plot{}
	for _, pg := range resolvePCBPages(b, pages) {
		layers := pg.layers
		if !pg.Stack {
			single[pg.ID] = &plot{layers: layers, frame: !opt.ExcludeDrawSheet}
			plots = append(plots, single[pg.ID])
			continue
		}
		rendered[pg.ID] = &RenderedPage{}
		for i, l := range layers {
			// The frame goes with the topmost layer only, so it isn't drawn once per layer.
			p := &plot{layers: []string{l}, frame: !opt.ExcludeDrawSheet && i == len(layers)-1}
			stacks[pg.ID] = append(stacks[pg.ID], p)
			plots = append(plots, p)
		}
	}
	parallel(len(plots), func(i int) { plots[i].out = renderBoardBuiltin(b, plots[i].layers, plots[i].frame) })
	for id, p := range single {
		rendered[id] = p.out
	}
	for id, ps := range stacks {
		stack := rendered[id]
		for _, p := range ps {
			stack.Layers = append(stack.Layers, &LayerSVG{Layer: p.layers[0], SVG: p.out.SVG})
			stack.Width, stack.Height = p.out.Width, p.out.Height
		}
	}
	return rendered
}

// padMargins holds board defaults in millimetres; ratio is per side, as in KiCad.
type padMargins struct{ mask, paste, ratio float64 }

func boardPadMargins(b *Board) padMargins {
	setup := b.Node.Child("setup")
	m := padMargins{num(setup.Child("pad_to_mask_clearance").Arg(0)), num(setup.Child("pad_to_paste_clearance").Arg(0)), num(setup.Child("pad_to_paste_clearance_ratio").Arg(0))}
	// Modern boards store these defaults in the project rather than the board setup.
	var pro struct {
		Board struct {
			DesignSettings struct {
				Rules struct {
					Mask  *float64 `json:"solder_mask_clearance"`
					Paste *float64 `json:"solder_paste_clearance"`
					Ratio *float64 `json:"solder_paste_margin_ratio"`
				} `json:"rules"`
			} `json:"design_settings"`
		} `json:"board"`
	}
	if json.Unmarshal(b.Files[path.Join(path.Dir(b.Root), b.Name+".kicad_pro")], &pro) == nil {
		rules := pro.Board.DesignSettings.Rules
		if rules.Mask != nil {
			m.mask = *rules.Mask
		}
		if rules.Paste != nil {
			m.paste = *rules.Paste
		}
		if rules.Ratio != nil {
			m.ratio = *rules.Ratio
		}
	}
	return m
}

func (r *boardLayer) padMargin(n, fp *Node, w, h float64) (float64, float64) {
	// KiCad 9 made an explicit zero an override. In older files zero inherits.
	legacy := num(r.Node.Child("version").Arg(0)) <= 20240201
	inherit := func(padKey, fpKey string, def float64) float64 {
		if v := n.Child(padKey); v != nil && (!legacy || num(v.Arg(0)) != 0) {
			return num(v.Arg(0))
		}
		v := fp.Child(fpKey)
		if v == nil && fpKey == "solder_paste_ratio" {
			v = fp.Child("solder_paste_margin_ratio")
		}
		if v != nil && (!legacy || num(v.Arg(0)) != 0) {
			return num(v.Arg(0))
		}
		return def
	}
	switch {
	case strings.HasSuffix(r.layer, ".Mask"):
		m := inherit("solder_mask_margin", "solder_mask_margin", r.margins.mask)
		return m, m
	case strings.HasSuffix(r.layer, ".Paste"):
		m := inherit("solder_paste_margin", "solder_paste_margin", r.margins.paste)
		ratio := inherit("solder_paste_margin_ratio", "solder_paste_ratio", r.margins.ratio)
		return m + w*ratio, m + h*ratio
	}
	return 0, 0
}

// textBox draws the optional border and wraps text using the bundled font metrics.
func (r *boardLayer) textBox(n *Node, t xform, fp *Node) {
	var corners [][2]float64
	if pts := n.Child("pts").Children("xy"); len(pts) == 4 {
		for _, p := range pts {
			x, y := xy(p)
			corners = append(corners, [2]float64{x, y})
		}
	} else {
		x1, y1 := xy(n.Child("start"))
		x2, y2 := xy(n.Child("end"))
		corners = [][2]float64{{x1, y1}, {x2, y1}, {x2, y2}, {x1, y2}}
	}
	var border strings.Builder
	for i, p := range corners {
		x, y := t.pt(p[0], p[1])
		cmd := "L"
		if i == 0 {
			cmd = "M"
		}
		fmt.Fprintf(&border, "%s%s %s", cmd, ff(x), ff(y))
	}
	border.WriteString("Z")
	stroke := n.Child("stroke")
	if stroke != nil && stroke.Child("type").Arg(0) != "none" && n.Child("border").Arg(0) != "no" {
		r.s.path(border.String(), r.color, num(stroke.Child("width").Arg(0)), "")
	}
	// Project corners into the text's axes. This also handles rotated four-point boxes.
	angle := num(n.Child("angle").Arg(0))
	sa, ca := math.Sincos(angle * math.Pi / 180)
	left, top, right, bottom := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, p := range corners {
		x, y := ca*p[0]-sa*p[1], sa*p[0]+ca*p[1]
		left = math.Min(left, x)
		right = math.Max(right, x)
		top = math.Min(top, y)
		bottom = math.Max(bottom, y)
	}
	st := effects(n, textStyle{size: 1, color: r.color, hj: -1, vj: -1})
	inset := st.size * 0.5
	if m := n.Child("margins"); m != nil {
		left, top, right, bottom = left+num(m.Arg(0)), top+num(m.Arg(1)), right-num(m.Arg(2)), bottom-num(m.Arg(3))
	} else {
		left, top, right, bottom = left+inset, top+inset, right-inset, bottom-inset
	}
	txt := n.Arg(0)
	if hasAtom(n, "locked") && txt == "locked" {
		txt = n.Arg(1)
	}
	if fp != n {
		txt = strings.NewReplacer("${REFERENCE}", propValue(fp, "Reference"), "${VALUE}", propValue(fp, "Value")).Replace(txt)
	}
	textSize := st.size
	if st.width > 0 {
		textSize = st.width
	}
	txt = wrapText(txt, math.Max(0, right-left), textSize)
	x, y := (left+right)/2, (top+bottom)/2
	if st.hj < 0 {
		x = left
	} else if st.hj > 0 {
		x = right
	}
	if st.vj < 0 {
		y = top
	} else if st.vj > 0 {
		y = bottom
	}
	x, y = t.pt(ca*x+sa*y, -sa*x+ca*y)
	st.angle = angle + math.Atan2(t.b, t.a)*180/math.Pi
	r.s.text(x, y, txt, st)
}

func wrapText(txt string, width, size float64) string {
	var lines []string
	for _, line := range strings.Split(txt, "\n") {
		current := ""
		for _, word := range strings.Fields(line) {
			candidate := word
			if current != "" {
				candidate = current + " " + word
			}
			if current != "" && textWidth(candidate, size) > width {
				lines = append(lines, current)
				current = word
			} else {
				current = candidate
			}
		}
		lines = append(lines, current)
	}
	return strings.Join(lines, "\n")
}
