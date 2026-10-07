package main

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Box is an axis-aligned rectangle in schematic millimetres.
type Box [4]float64 // x1, y1, x2, y2

func pointBox(x, y, r float64) *Box { return &Box{x - r, y - r, x + r, y + r} }

// Component is a placed symbol as seen on one sheet instance.
type Component struct {
	UUID      string
	Ref       string
	Unit      string
	LibID     string
	X, Y, Rot float64
	Mirror    string
	Props     map[string]string // field name -> value (Reference excluded)
	PropOrder []string
	FieldPos  string // serialized positions of all fields, for detecting cosmetic moves
	Flags     map[string]string
	LibDef    string // serialized embedded library symbol, for detecting symbol updates
}

func (c *Component) IsPower() bool { return strings.HasPrefix(c.Ref, "#") }

// Item is any other schematic object (wire, label, junction, text, ...).
type Item struct {
	UUID    string
	Kind    string
	Text    string // label/text content, sheet name
	Content string // canonical serialization for change detection
	Geom    string // geometry part of the content
	Rest    string // non-geometry part of the content
	Box     Box
}

// SheetContent holds the extracted objects of one sheet instance.
type SheetContent struct {
	Components []*Component
	Items      []*Item
}

var itemKinds = map[string]bool{
	"wire": true, "bus": true, "bus_entry": true, "junction": true, "no_connect": true,
	"label": true, "global_label": true, "hierarchical_label": true, "netclass_flag": true,
	"directive_label": true, "text": true, "text_box": true, "sheet": true, "polyline": true,
	"rectangle": true, "circle": true, "arc": true, "bezier": true, "image": true, "table": true,
	"rule_area": true,
}

// Extract collects the objects of sheet inst in project p.
func Extract(p *Project, inst *SheetInst) *SheetContent {
	sch := p.Parsed[inst.File]
	libDefs := map[string]string{}
	if ls := sch.Child("lib_symbols"); ls != nil {
		for _, s := range ls.Children("symbol") {
			libDefs[s.Arg(0)] = s.String()
		}
	}
	sc := &SheetContent{}
	for _, n := range sch.List {
		if !n.IsList {
			continue
		}
		head := n.Head()
		switch {
		case head == "symbol":
			sc.Components = append(sc.Components, extractComponent(n, inst, libDefs))
		case itemKinds[head]:
			sc.Items = append(sc.Items, extractItem(n))
		}
	}
	return sc
}

func extractComponent(n *Node, inst *SheetInst, libDefs map[string]string) *Component {
	c := &Component{
		UUID:  n.Child("uuid").Arg(0),
		LibID: n.Child("lib_id").Arg(0),
		Unit:  n.Child("unit").Arg(0),
		Props: map[string]string{},
		Flags: map[string]string{},
	}
	if at := n.Child("at"); at != nil {
		c.X, c.Y, c.Rot = num(at.Arg(0)), num(at.Arg(1)), num(at.Arg(2))
	}
	if m := n.Child("mirror"); m != nil {
		c.Mirror = m.Arg(0)
	}
	for _, f := range []string{"in_bom", "on_board", "dnp", "exclude_from_sim", "body_style", "in_pos_files"} {
		if fn := n.Child(f); fn != nil {
			c.Flags[f] = fn.Arg(0)
		}
	}
	var fieldPos []string
	for _, pr := range n.Children("property") {
		name, val := pr.Arg(0), pr.Arg(1)
		pos := ""
		if at := pr.Child("at"); at != nil {
			pos = at.String()
		}
		if pr.Child("hide") != nil && pr.Child("hide").Arg(0) == "yes" {
			pos += " hidden"
		}
		fieldPos = append(fieldPos, name+"@"+pos)
		if name == "Reference" {
			c.Ref = val
			continue
		}
		c.Props[name] = val
		c.PropOrder = append(c.PropOrder, name)
	}
	c.FieldPos = strings.Join(fieldPos, ";")
	// Per-instance reference/unit for reused sheets.
	if is := n.Child("instances"); is != nil {
		for _, proj := range is.Children("project") {
			for _, pth := range proj.Children("path") {
				if pth.Arg(0) == inst.UUIDPath {
					if r := pth.Child("reference"); r != nil {
						c.Ref = r.Arg(0)
					}
					if u := pth.Child("unit"); u != nil {
						c.Unit = u.Arg(0)
					}
				}
			}
		}
	}
	libName := c.LibID
	if ln := n.Child("lib_name"); ln != nil {
		libName = ln.Arg(0)
	}
	c.LibDef = libDefs[libName]
	return c
}

func extractItem(n *Node) *Item {
	it := &Item{Kind: n.Head(), UUID: n.Child("uuid").Arg(0)}
	// Content without the uuid, so identical objects compare equal.
	var parts, geom, rest []string
	for _, c := range n.List[1:] {
		if c.IsList && c.Head() == "uuid" {
			continue
		}
		s := c.String()
		parts = append(parts, s)
		switch c.Head() {
		case "at", "pts", "size", "start", "end", "center", "mid", "radius":
			geom = append(geom, s)
		default:
			rest = append(rest, s)
		}
	}
	it.Rest = strings.Join(rest, " ")
	it.Content = strings.Join(parts, " ")
	it.Geom = strings.Join(geom, " ")
	switch it.Kind {
	case "label", "global_label", "hierarchical_label", "text", "directive_label", "netclass_flag":
		it.Text = n.Arg(0)
	case "text_box":
		it.Text = n.Arg(0)
	case "sheet":
		it.Text = propValue(n, "Sheetname")
	}
	it.Box = itemBox(n)
	return it
}

// itemBox estimates a bounding box from whatever coordinates the object carries.
func itemBox(n *Node) Box {
	var xs, ys []float64
	add := func(x, y float64) { xs = append(xs, x); ys = append(ys, y) }
	if pts := n.Child("pts"); pts != nil {
		for _, xy := range pts.Children("xy") {
			add(num(xy.Arg(0)), num(xy.Arg(1)))
		}
	}
	for _, h := range []string{"at", "start", "end", "center", "mid"} {
		if c := n.Child(h); c != nil && n.Head() != "symbol" {
			add(num(c.Arg(0)), num(c.Arg(1)))
		}
	}
	if at, sz := n.Child("at"), n.Child("size"); at != nil && sz != nil {
		add(num(at.Arg(0))+num(sz.Arg(0)), num(at.Arg(1))+num(sz.Arg(1)))
	}
	if r := n.Child("radius"); r != nil {
		if c := n.Child("center"); c != nil {
			rad := num(r.Arg(0))
			add(num(c.Arg(0))-rad, num(c.Arg(1))-rad)
			add(num(c.Arg(0))+rad, num(c.Arg(1))+rad)
		}
	}
	if len(xs) == 0 {
		return Box{}
	}
	b := Box{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for i := range xs {
		b[0], b[1] = math.Min(b[0], xs[i]), math.Min(b[1], ys[i])
		b[2], b[3] = math.Max(b[2], xs[i]), math.Max(b[3], ys[i])
	}
	// Labels/text extend from their anchor; give them some room.
	if n.Head() != "wire" && n.Head() != "bus" {
		pad := 1.5
		if len(n.Arg(0)) > 0 {
			pad = math.Max(pad, float64(len(n.Arg(0)))*0.7)
		}
		b = Box{b[0] - pad, b[1] - pad, b[2] + pad, b[3] + pad}
	}
	return b
}

func num(s string) float64 {
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func fmtPos(x, y float64) string { return fmt.Sprintf("(%s, %s)", fmtNum(x), fmtNum(y)) }

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
