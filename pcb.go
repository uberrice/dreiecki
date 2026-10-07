package main

import (
	"bytes"
	"fmt"
	"math"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Board is one revision of a .kicad_pcb.
type Board struct {
	*Project // Files and Root for rendering; Sheets is unused
	Node     *Node
	Layers   []string          // canonical layer names from the layer table
	Copper   []string          // copper layers, front to back
	UserName map[string]string // canonical -> user-visible layer name
	Nets     map[string]string // net number -> name
	Used     map[string]bool   // layers some object is drawn on
}

// LoadBoard reads a board and the project files rendering may need from src.
func LoadBoard(src Source, rel string) (*Board, error) {
	p := &Project{
		Name:   strings.TrimSuffix(path.Base(rel), ".kicad_pcb"),
		Root:   rel,
		Files:  map[string][]byte{},
		Parsed: map[string]*Node{},
	}
	data, err := src.ReadFile(rel)
	if err != nil {
		return nil, fmt.Errorf("%s does not exist in this revision", rel)
	}
	n, err := ParseSexpr(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rel, err)
	}
	if n.Head() != "kicad_pcb" {
		return nil, fmt.Errorf("%s is not a KiCad board", rel)
	}
	p.Files[rel] = data
	if err := p.loadAux(src); err != nil {
		return nil, err
	}
	b := &Board{Project: p, Node: n, UserName: map[string]string{}, Nets: map[string]string{}, Used: map[string]bool{}}
	if ls := n.Child("layers"); ls != nil {
		for _, l := range ls.List[1:] {
			if !l.IsList || len(l.List) < 3 || l.List[1].IsList {
				continue
			}
			name := l.List[1].Atom
			b.Layers = append(b.Layers, name)
			if len(l.List) > 3 && !l.List[3].IsList {
				b.UserName[name] = l.List[3].Atom
			}
			if strings.HasSuffix(name, ".Cu") {
				b.Copper = append(b.Copper, name)
			}
		}
	}
	sort.SliceStable(b.Copper, func(i, j int) bool { return copperRank(b.Copper[i]) < copperRank(b.Copper[j]) })
	for _, c := range n.Children("net") {
		b.Nets[c.Arg(0)] = c.Arg(1)
	}
	for _, c := range n.List[1:] {
		if c.IsList && c.Head() != "layers" && c.Head() != "setup" {
			b.markUsed(c)
		}
	}
	return b, nil
}

// copperRank orders F.Cu, In1.Cu, In2.Cu, ..., B.Cu.
func copperRank(l string) int {
	switch l {
	case "F.Cu":
		return 0
	case "B.Cu":
		return 1 << 20
	}
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(l, "In"), ".Cu"))
	return 1 + n
}

func (b *Board) has(layer string) bool {
	for _, l := range b.Layers {
		if l == layer {
			return true
		}
	}
	return false
}

func (b *Board) layerName(l string) string {
	if u := b.UserName[l]; u != "" {
		return u
	}
	return l
}

func (b *Board) markUsed(n *Node) {
	if h := n.Head(); h == "layer" || h == "layers" {
		for _, l := range b.expand(n) {
			b.Used[l] = true
		}
	}
	for _, c := range n.List {
		if c.IsList {
			b.markUsed(c)
		}
	}
}

// expand resolves the arguments of a (layer ...) or (layers ...) node,
// including wildcards such as *.Cu and F&B.Cu, to canonical layer names.
func (b *Board) expand(n *Node) []string {
	var out []string
	for i := 0; n.Arg(i) != ""; i++ {
		l := n.Arg(i)
		switch {
		case l == "*.Cu":
			out = append(out, b.Copper...)
		case l == "*In.Cu":
			for _, c := range b.Copper {
				if strings.HasPrefix(c, "In") {
					out = append(out, c)
				}
			}
		case strings.HasPrefix(l, "*."):
			out = append(out, "F"+l[1:], "B"+l[1:])
		case strings.HasPrefix(l, "F&B."):
			out = append(out, "F"+l[3:], "B"+l[3:])
		default:
			out = append(out, l)
		}
	}
	return out
}

// nodeLayers lists the layers an object is on. A via spans its copper range.
func (b *Board) nodeLayers(n *Node) []string {
	var out []string
	if l := n.Child("layer"); l != nil {
		out = append(out, b.expand(l)...)
	}
	if l := n.Child("layers"); l != nil {
		ls := b.expand(l)
		if n.Head() == "via" && len(ls) == 2 {
			i, j := indexOf(b.Copper, ls[0]), indexOf(b.Copper, ls[1])
			if i >= 0 && j >= 0 {
				ls = append([]string{}, b.Copper[min(i, j):max(i, j)+1]...)
			}
		}
		out = append(out, ls...)
	}
	if n.Head() == "via" && len(out) == 0 {
		out = append(out, b.Copper...)
	}
	return union(out, nil)
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func (b *Board) netName(n *Node) string {
	if n.Arg(1) != "" {
		return n.Arg(1)
	}
	if name, ok := b.Nets[n.Arg(0)]; ok {
		return name
	}
	return n.Arg(0)
}

// Volatile or derived data that should not count as a change.
var skipHeads = map[string]bool{"uuid": true, "tstamp": true, "filled_polygon": true, "fill_segments": true}

// canon serializes n like Node.String, without uuids and zone fills, and with
// nets by name so renumbering doesn't make every track look changed.
func (b *Board) canon(n *Node) string {
	var sb strings.Builder
	b.write(&sb, n, false)
	return sb.String()
}

func (b *Board) write(sb *strings.Builder, n *Node, dropNet bool) {
	if !n.IsList {
		n.write(sb)
		return
	}
	if n.Head() == "net" {
		fmt.Fprintf(sb, "(net %q)", b.netName(n))
		return
	}
	sb.WriteByte('(')
	first := true
	for _, c := range n.List {
		if c.IsList && (skipHeads[c.Head()] || dropNet && c.Head() == "net") {
			continue
		}
		if !first {
			sb.WriteByte(' ')
		}
		first = false
		b.write(sb, c, false)
	}
	sb.WriteByte(')')
}

// BoardContent holds the extracted objects of a board.
type BoardContent struct {
	Footprints []*Component
	Items      []*Item
}

var pcbItemKinds = map[string]string{
	"segment": "track", "arc": "track", "via": "via", "zone": "zone",
	"gr_text": "text", "gr_text_box": "text", "table": "text",
	"gr_line": "graphic", "gr_rect": "graphic", "gr_circle": "graphic", "gr_arc": "graphic",
	"gr_poly": "graphic", "gr_curve": "graphic", "dimension": "graphic", "image": "graphic", "target": "graphic",
}

var pcbKindNames = map[string]string{
	"segment": "track", "arc": "track arc", "gr_line": "line", "gr_rect": "rectangle", "gr_circle": "circle",
	"gr_arc": "arc", "gr_poly": "polygon", "gr_curve": "curve", "gr_text": "text", "gr_text_box": "text box",
}

func (b *Board) Extract() *BoardContent {
	bc := &BoardContent{}
	for _, n := range b.Node.List {
		if !n.IsList {
			continue
		}
		switch h := n.Head(); {
		case h == "footprint" || h == "module":
			bc.Footprints = append(bc.Footprints, b.footprint(n))
		case pcbItemKinds[h] != "":
			bc.Items = append(bc.Items, b.item(n))
		}
	}
	return bc
}

func uuidOf(n *Node) string {
	if u := n.Child("uuid"); u != nil {
		return u.Arg(0)
	}
	return n.Child("tstamp").Arg(0)
}

func (b *Board) footprint(n *Node) *Component {
	c := &Component{
		Footprint: true, UUID: uuidOf(n), LibID: n.Arg(0),
		Props: map[string]string{}, Flags: map[string]string{}, Pads: map[string]string{},
	}
	if at := n.Child("at"); at != nil {
		c.X, c.Y, c.Rot = num(at.Arg(0)), num(at.Arg(1)), num(at.Arg(2))
	}
	layer := n.Child("layer").Arg(0)
	c.Flags["Side"] = "front"
	if strings.HasPrefix(layer, "B.") {
		c.Flags["Side"] = "back"
	}
	if a := n.Child("attr"); a != nil {
		var attrs []string
		for i := 0; a.Arg(i) != ""; i++ {
			attrs = append(attrs, a.Arg(i))
		}
		c.Flags["Attributes"] = strings.Join(attrs, " ")
	}
	locked := n.Child("locked") != nil && n.Child("locked").Arg(0) != "no"
	for _, x := range n.List {
		locked = locked || !x.IsList && !x.Quoted && x.Atom == "locked"
	}
	if locked {
		c.Flags["Locked"] = "yes"
	}

	var fieldPos, geom []string
	layers := []string{layer}
	pts := &bounds{}
	for _, ch := range n.List[1:] {
		if !ch.IsList {
			continue
		}
		h := ch.Head()
		// Fields: (property "Value" "10k" ...) in KiCad 8+, (fp_text value "10k" ...) before.
		name, val, field := "", "", false
		switch {
		case h == "property":
			name, val, field = ch.Arg(0), ch.Arg(1), true
		case h == "fp_text" && (ch.Arg(0) == "reference" || ch.Arg(0) == "value"):
			name, val, field = strings.ToUpper(ch.Arg(0)[:1])+ch.Arg(0)[1:], ch.Arg(1), true
		}
		if field {
			pos := orEmpty(ch.Child("at")).String() + " " + ch.Child("layer").Arg(0)
			if hd := ch.Child("hide"); hd != nil && hd.Arg(0) != "no" || hasAtom(ch, "hide") {
				pos += " hidden"
			}
			fieldPos = append(fieldPos, name+"@"+pos)
			if name == "Reference" {
				c.Ref = val
			} else if _, dup := c.Props[name]; !dup {
				c.Props[name] = val
				c.PropOrder = append(c.PropOrder, name)
			}
			continue
		}
		if fpMeta[h] {
			continue
		}
		layers = append(layers, b.nodeLayers(ch)...)
		pts.addLocal(ch)
		if h == "pad" {
			k := ch.Arg(0)
			for i := 2; ; i++ {
				if _, dup := c.Pads[k]; !dup {
					break
				}
				k = fmt.Sprintf("%s#%d", ch.Arg(0), i)
			}
			c.Pads[k] = ""
			if nt := ch.Child("net"); nt != nil {
				c.Pads[k] = b.netName(nt)
			}
			c.PadOrder = append(c.PadOrder, k)
		}
		var sb strings.Builder
		b.write(&sb, relAngle(ch, c.Rot), h == "pad")
		geom = append(geom, sb.String())
	}
	c.FieldPos = strings.Join(fieldPos, ";")
	c.LibDef = strings.Join(geom, " ")
	c.Layers = union(layers, nil)
	if box, ok := pts.box(c.X, c.Y, c.Rot, 0.3); ok {
		c.Box = &box
	} else {
		c.Box = pointBox(c.X, c.Y, 2)
	}
	return c
}

func hasAtom(n *Node, a string) bool {
	for _, x := range n.List {
		if !x.IsList && !x.Quoted && x.Atom == a {
			return true
		}
	}
	return false
}

// Footprint children that are placement or metadata rather than geometry.
var fpMeta = map[string]bool{
	"at": true, "layer": true, "path": true, "sheetname": true, "sheetfile": true, "attr": true, "locked": true,
	"uuid": true, "tstamp": true, "descr": true, "tags": true, "placed": true, "tedit": true,
}

// relAngle makes a footprint child's (at x y angle) relative to the footprint
// rotation, so rotating a footprint doesn't count as changing its pads.
func relAngle(n *Node, rot float64) *Node {
	at := n.Child("at")
	if at == nil {
		return n
	}
	a := math.Mod(num(at.Arg(2))-rot+720, 360)
	nat := &Node{IsList: true, List: []*Node{{Atom: "at"}, {Atom: at.Arg(0)}, {Atom: at.Arg(1)}}}
	if a != 0 {
		nat.List = append(nat.List, &Node{Atom: fmtNum(math.Round(a*1000) / 1000)})
	}
	cp := *n
	cp.List = make([]*Node, len(n.List))
	for i, x := range n.List {
		if x == at {
			x = nat
		}
		cp.List[i] = x
	}
	return &cp
}

// bounds collects points in footprint-local coordinates.
type bounds struct{ xs, ys []float64 }

func (p *bounds) add(x, y float64) { p.xs = append(p.xs, x); p.ys = append(p.ys, y) }

func (p *bounds) addLocal(n *Node) {
	pt := func(h string) (float64, float64, bool) {
		if c := n.Child(h); c != nil {
			return num(c.Arg(0)), num(c.Arg(1)), true
		}
		return 0, 0, false
	}
	switch n.Head() {
	case "pad":
		x, y, _ := pt("at")
		sx, sy, _ := pt("size")
		r := math.Max(sx, sy) / 2
		p.add(x-r, y-r)
		p.add(x+r, y+r)
	case "fp_circle":
		cx, cy, _ := pt("center")
		ex, ey, _ := pt("end")
		r := math.Hypot(ex-cx, ey-cy)
		p.add(cx-r, cy-r)
		p.add(cx+r, cy+r)
	default:
		for _, h := range []string{"start", "mid", "end"} {
			if x, y, ok := pt(h); ok {
				p.add(x, y)
			}
		}
		for _, xy := range n.Child("pts").Children("xy") {
			p.add(num(xy.Arg(0)), num(xy.Arg(1)))
		}
	}
}

// box rotates and translates the local points into board coordinates and adds pad around them.
func (p *bounds) box(x, y, rot, pad float64) (Box, bool) {
	if len(p.xs) == 0 {
		return Box{}, false
	}
	s, c := math.Sincos(rot * math.Pi / 180)
	b := Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for i := range p.xs {
		bx, by := x+p.xs[i]*c+p.ys[i]*s, y-p.xs[i]*s+p.ys[i]*c
		b[0], b[1] = math.Min(b[0], bx), math.Min(b[1], by)
		b[2], b[3] = math.Max(b[2], bx), math.Max(b[3], by)
	}
	return Box{b[0] - pad, b[1] - pad, b[2] + pad, b[3] + pad}, true
}

var attrNames = map[string]string{
	"net": "Net", "layer": "Layer", "layers": "Layers", "width": "Width", "size": "Size",
	"drill": "Drill", "priority": "Priority", "name": "Name",
}

func (b *Board) item(n *Node) *Item {
	it := &Item{Kind: n.Head(), UUID: uuidOf(n), Category: pcbItemKinds[n.Head()], Attrs: map[string]string{}}
	var parts, geom, rest []string
	for _, c := range n.List[1:] {
		if c.IsList && skipHeads[c.Head()] {
			continue
		}
		s := b.canon(c)
		parts = append(parts, s)
		switch c.Head() {
		case "at", "start", "end", "center", "mid", "pts", "polygon", "radius":
			geom = append(geom, s)
		default:
			rest = append(rest, s)
		}
		if name := attrNames[c.Head()]; name != "" && c.IsList {
			if c.Head() == "net" {
				it.Attrs[name] = b.netName(c)
			} else {
				var args []string
				for _, x := range c.List[1:] {
					if x.IsList {
						args = append(args, x.String())
					} else {
						args = append(args, x.Atom)
					}
				}
				it.Attrs[name] = strings.Join(args, " ")
			}
		}
	}
	it.Content = strings.Join(parts, " ")
	it.Geom = strings.Join(geom, " ")
	it.Rest = strings.Join(rest, " ")
	it.Layers = b.nodeLayers(n)
	switch it.Kind {
	case "gr_text", "gr_text_box":
		it.Text = n.Arg(0)
	}

	name := pcbKindNames[it.Kind]
	if name == "" {
		name = it.Kind
	}
	if it.Text != "" {
		t := strings.Join(strings.Fields(it.Text), " ")
		if len(t) > 40 {
			t = t[:40] + "…"
		}
		name += " “" + t + "”"
	}
	if net := it.Attrs["Net"]; net != "" {
		name += " " + net
	} else if nn := n.Child("net_name"); nn != nil && nn.Arg(0) != "" {
		name += " " + nn.Arg(0)
	}
	if zn := n.Child("name"); zn != nil && it.Kind == "zone" {
		name += " “" + zn.Arg(0) + "”"
	}
	var lnames []string
	for _, l := range it.Layers {
		lnames = append(lnames, b.layerName(l))
	}
	if len(lnames) > 0 && len(lnames) <= 3 {
		name += " (" + strings.Join(lnames, ", ") + ")"
	}
	it.Title = name

	// Bounding box and a short position description.
	pt := func(h string) (float64, float64, bool) {
		if c := n.Child(h); c != nil {
			return num(c.Arg(0)), num(c.Arg(1)), true
		}
		return 0, 0, false
	}
	p := &bounds{}
	for _, h := range []string{"at", "start", "mid", "end"} {
		if x, y, ok := pt(h); ok {
			p.add(x, y)
		}
	}
	if it.Kind == "gr_circle" {
		cx, cy, _ := pt("center")
		ex, ey, _ := pt("end")
		r := math.Hypot(ex-cx, ey-cy)
		p.add(cx-r, cy-r)
		p.add(cx+r, cy+r)
	}
	var polys []*Node
	polys = append(polys, n.Child("pts"))
	for _, pg := range n.Children("polygon") {
		polys = append(polys, pg.Child("pts"))
	}
	for _, pl := range polys {
		for _, xy := range pl.Children("xy") {
			p.add(num(xy.Arg(0)), num(xy.Arg(1)))
		}
	}
	pad := 0.3
	if w := n.Child("width"); w != nil {
		pad = math.Max(pad, num(w.Arg(0))/2)
	}
	if s := n.Child("size"); s != nil && it.Kind == "via" {
		pad = math.Max(pad, num(s.Arg(0))/2)
	}
	if it.Category == "text" {
		pad = math.Max(1.5, float64(len(it.Text))*0.5)
	}
	it.Box, _ = p.box(0, 0, 0, pad)
	switch it.Kind {
	case "segment", "arc":
		sx, sy, _ := pt("start")
		ex, ey, _ := pt("end")
		it.Desc = fmt.Sprintf("%s–%s", fmtPos(sx, sy), fmtPos(ex, ey))
	case "via":
		x, y, _ := pt("at")
		it.Desc = "at " + fmtPos(x, y)
	default:
		it.Desc = fmt.Sprintf("near %s", fmtPos(round1((it.Box[0]+it.Box[2])/2), round1((it.Box[1]+it.Box[3])/2)))
	}
	return it
}

// diffBoard compares nets and board-level settings, which have no position.
func diffBoard(a, b *Board) []Change {
	var changes []Change
	netsA, netsB := map[string]bool{}, map[string]bool{}
	for _, n := range a.Nets {
		netsA[n] = n != ""
	}
	for _, n := range b.Nets {
		netsB[n] = n != ""
	}
	for _, n := range sortedKeys(boolKeys(netsA)) {
		if netsA[n] && !netsB[n] {
			changes = append(changes, Change{Category: "net", Op: "removed", Title: n})
		}
	}
	for _, n := range sortedKeys(boolKeys(netsB)) {
		if netsB[n] && !netsA[n] {
			changes = append(changes, Change{Category: "net", Op: "added", Title: n})
		}
	}

	// Title block, field by field.
	fields := func(bd *Board) (map[string]string, []string) {
		m := map[string]string{}
		var order []string
		for _, c := range orEmpty(bd.Node.Child("title_block")).List {
			if !c.IsList {
				continue
			}
			k := c.Head()
			if k == "comment" {
				k += " " + c.Arg(0)
				m[k] = c.Arg(1)
			} else {
				m[k] = c.Arg(0)
			}
			order = append(order, k)
		}
		return m, order
	}
	ta, oa := fields(a)
	tb, ob := fields(b)
	var details []string
	for _, k := range union(oa, ob) {
		if ta[k] != tb[k] {
			details = append(details, fmt.Sprintf("%s: %s → %s", k, quoteEmpty(ta[k]), quoteEmpty(tb[k])))
		}
	}
	if len(details) > 0 {
		changes = append(changes, Change{Category: "board", Op: "changed", Title: "Title block", Details: details})
	}
	for _, s := range []struct{ head, title string }{
		{"paper", "Page settings"}, {"general", "Board thickness"}, {"layers", "Layer table"}, {"setup", "Board setup / stackup"},
	} {
		if a.canon(orEmpty(a.Node.Child(s.head))) != b.canon(orEmpty(b.Node.Child(s.head))) {
			changes = append(changes, Change{Category: "board", Op: "changed", Title: s.title})
		}
	}
	return changes
}

func orEmpty(n *Node) *Node {
	if n == nil {
		return &Node{IsList: true}
	}
	return n
}

func boolKeys(m map[string]bool) map[string]string {
	out := map[string]string{}
	for k := range m {
		out[k] = ""
	}
	return out
}

// pcbPage is one page of the PCB report: an overview or a single layer.
type pcbPage struct {
	ID     string
	Name   string
	Layer  string   // the layer whose changes are listed; "" for the overview
	Layers []string // layers to plot
}

// Non-copper layers in the order they are listed, if anything is drawn on them.
var techLayers = []string{
	"F.SilkS", "B.SilkS", "F.Mask", "B.Mask", "F.Paste", "B.Paste", "F.Adhes", "B.Adhes",
	"F.Fab", "B.Fab", "F.CrtYd", "B.CrtYd", "Edge.Cuts", "Margin",
	"Dwgs.User", "Cmts.User", "Eco1.User", "Eco2.User",
}

func planPages(a, b *Board) []pcbPage {
	copper := union(b.Copper, a.Copper)
	sort.SliceStable(copper, func(i, j int) bool { return copperRank(copper[i]) < copperRank(copper[j]) })
	pages := []pcbPage{{ID: "all", Name: "All layers", Layers: append(append([]string{}, copper...), "F.SilkS", "B.SilkS", "Edge.Cuts")}}
	for _, l := range copper {
		pages = append(pages, pcbPage{ID: l, Layer: l, Layers: []string{l, "Edge.Cuts"}})
	}
	tech := append([]string{}, techLayers...)
	for _, l := range union(b.Layers, a.Layers) {
		if !strings.HasSuffix(l, ".Cu") && indexOf(tech, l) < 0 {
			tech = append(tech, l) // User.1 ...
		}
	}
	for _, l := range tech {
		if a.Used[l] && a.has(l) || b.Used[l] && b.has(l) {
			pages = append(pages, pcbPage{ID: l, Layer: l, Layers: []string{l, "Edge.Cuts"}})
		}
	}
	for i := range pages {
		if pages[i].Layer != "" {
			pages[i].Name = b.layerName(pages[i].Layer)
			if !b.has(pages[i].Layer) {
				pages[i].Name = a.layerName(pages[i].Layer)
			}
		}
	}
	return pages
}

func buildPCBReport(ba, bb *Board, pages []pcbPage, ra, rb map[string]*RenderedPage) *Report {
	ca, cb := ba.Extract(), bb.Extract()
	all := append(diffComponents(ca.Footprints, cb.Footprints), diffItems(ca.Items, cb.Items)...)
	all = append(all, diffBoard(ba, bb)...)
	sortChanges(all)

	rep := &Report{Kind: "pcb"}
	for i, pg := range pages {
		page := PageReport{ID: fmt.Sprintf("p%d", i), Name: pg.Name, File: bb.Root, Changes: []Change{}}
		for _, c := range all {
			if pg.Layer == "" || indexOf(c.layers, pg.Layer) >= 0 {
				page.Changes = append(page.Changes, c)
			}
		}
		svgA, svgB := ra[pg.ID], rb[pg.ID]
		for _, sp := range []*RenderedPage{svgA, svgB} {
			if sp != nil {
				page.Width, page.Height = sp.Width, sp.Height
			}
		}
		if svgA != nil {
			page.SvgA = packSVG(svgA.SVG)
		}
		if svgB != nil {
			page.SvgB = packSVG(svgB.SVG)
		}
		switch {
		case svgA == nil && svgB == nil:
			continue
		case svgA == nil:
			page.Status = "added"
		case svgB == nil:
			page.Status = "removed"
		case len(page.Changes) > 0 || !bytes.Equal(stripVolatile(svgA.SVG), stripVolatile(svgB.SVG)):
			page.Status = "changed"
		default:
			page.Status = "same"
		}
		rep.Pages = append(rep.Pages, page)
	}
	return rep
}
