package main

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
)

// Change is one semantic difference shown in the report's change list.
type Change struct {
	Category string   `json:"category"` // component, power, label, wire, junction, text, sheet, graphic; for PCBs footprint, track, via, zone, net, board
	Op       string   `json:"op"`       // added, removed, changed, moved
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Details  []string `json:"details,omitempty"`
	BoxA     *Box     `json:"boxA,omitempty"`
	BoxB     *Box     `json:"boxB,omitempty"`

	layers []string // PCB layers the change touches, for per-layer pages
}

const symbolRadius = 6.0

func diffComponents(a, b []*Component) []Change {
	var changes []Change
	byUUID := map[string][]*Component{}
	for _, c := range b {
		if c.UUID != "" {
			byUUID[c.UUID] = append(byUUID[c.UUID], c)
		}
	}
	matchedB := map[*Component]bool{}
	var unmatchedA []*Component
	pairs := [][2]*Component{}
	for _, ca := range a {
		// A repeated identifier is ambiguous and must not be paired by uuid.
		cands := byUUID[ca.UUID]
		if len(cands) == 1 && !matchedB[cands[0]] {
			matchedB[cands[0]] = true
			pairs = append(pairs, [2]*Component{ca, cands[0]})
		} else {
			unmatchedA = append(unmatchedA, ca)
		}
	}
	// Fall back to reference designator + unit (e.g. part deleted and re-placed).
	var unmatchedB []*Component
	for _, cb := range b {
		if !matchedB[cb] {
			unmatchedB = append(unmatchedB, cb)
		}
	}
	refPairs := matchByRef(unmatchedA, unmatchedB)
	matchedA := map[*Component]bool{}
	for _, p := range refPairs {
		matchedA[p[0]] = true
		matchedB[p[1]] = true
		pairs = append(pairs, p)
	}
	for _, p := range pairs {
		if ch, ok := compareComponent(p[0], p[1]); ok {
			changes = append(changes, ch)
		}
	}
	var removed []*Component
	for _, ca := range unmatchedA {
		if !matchedA[ca] {
			removed = append(removed, ca)
		}
	}
	for _, ca := range removed {
		changes = append(changes, Change{
			Category: componentCategory(ca), Op: "removed", Title: refTitle(ca),
			Subtitle: componentSubtitle(ca), BoxA: ca.box(), layers: ca.Layers,
		})
	}
	for _, cb := range b {
		if !matchedB[cb] {
			changes = append(changes, Change{
				Category: componentCategory(cb), Op: "added", Title: refTitle(cb),
				Subtitle: componentSubtitle(cb), BoxB: cb.box(), layers: cb.Layers,
			})
		}
	}
	return changes
}

// matchByRef pairs components by reference designator and unit. An ambiguous
// reference pairs unchanged parts first, then parts with a unique library id,
// so the file order never decides which change is reported.
func matchByRef(a, b []*Component) [][2]*Component {
	groupA := map[string][]*Component{}
	groupB := map[string][]*Component{}
	for _, c := range a {
		if strings.HasSuffix(c.Ref, "?") {
			continue
		}
		groupA[refKey(c)] = append(groupA[refKey(c)], c)
	}
	for _, c := range b {
		groupB[refKey(c)] = append(groupB[refKey(c)], c)
	}
	var pairs [][2]*Component
	for _, key := range slices.Sorted(maps.Keys(groupA)) {
		ga, gb := groupA[key], groupB[key]
		if len(ga) == 1 && len(gb) == 1 {
			pairs = append(pairs, [2]*Component{ga[0], gb[0]})
			continue
		}
		unchanged, restA, restB := pairUnchanged(ga, gb)
		pairs = append(pairs, unchanged...)
		for _, ca := range restA {
			cands := withLibID(restB, ca.LibID)
			if len(cands) != 1 || len(withLibID(restA, ca.LibID)) != 1 {
				continue
			}
			pairs = append(pairs, [2]*Component{ca, cands[0]})
		}
	}
	return pairs
}

// pairUnchanged pairs identical components and returns the leftovers.
func pairUnchanged(ga, gb []*Component) (pairs [][2]*Component, restA, restB []*Component) {
	usedB := map[*Component]bool{}
	for _, ca := range ga {
		var partner *Component
		for _, cb := range gb {
			if !usedB[cb] && sameComponent(ca, cb) {
				partner = cb
				break
			}
		}
		if partner == nil {
			restA = append(restA, ca)
			continue
		}
		usedB[partner] = true
		pairs = append(pairs, [2]*Component{ca, partner})
	}
	for _, cb := range gb {
		if !usedB[cb] {
			restB = append(restB, cb)
		}
	}
	return pairs, restA, restB
}

func sameComponent(a, b *Component) bool {
	return a.LibID == b.LibID && maps.Equal(a.Props, b.Props) &&
		maps.Equal(a.Flags, b.Flags) && a.Mirror == b.Mirror &&
		samePos(a.X, b.X) && samePos(a.Y, b.Y) && sameAngle(a.Rot, b.Rot)
}

func refKey(c *Component) string { return c.Ref + "/" + c.Unit }

func withLibID(cs []*Component, libID string) []*Component {
	var out []*Component
	for _, c := range cs {
		if c.LibID == libID {
			out = append(out, c)
		}
	}
	return out
}

func compareComponent(a, b *Component) (Change, bool) {
	var details []string
	moved := false
	if a.Ref != b.Ref {
		details = append(details, fmt.Sprintf("Reference: %s → %s", a.Ref, b.Ref))
	}
	kind, def := "Symbol", "Library symbol definition updated (pins/graphics)"
	if a.Footprint {
		kind, def = "Footprint", "Footprint geometry changed (pads/graphics)"
	}
	if a.LibID != b.LibID {
		details = append(details, fmt.Sprintf("%s: %s → %s", kind, a.LibID, b.LibID))
	} else if a.LibDef != b.LibDef && a.LibDef != "" && b.LibDef != "" {
		details = append(details, def)
	}
	if a.Unit != b.Unit {
		details = append(details, fmt.Sprintf("Unit: %s → %s", a.Unit, b.Unit))
	}
	// Fields, in a stable order: A's order, then fields new in B.
	seen := map[string]bool{}
	order := append([]string{}, a.PropOrder...)
	order = append(order, b.PropOrder...)
	for _, k := range order {
		if seen[k] {
			continue
		}
		seen[k] = true
		va, okA := a.Props[k]
		vb, okB := b.Props[k]
		switch {
		case okA && !okB:
			details = append(details, fmt.Sprintf("%s removed (was %q)", k, va))
		case !okA && okB:
			details = append(details, fmt.Sprintf("%s added: %q", k, vb))
		case va != vb:
			details = append(details, fmt.Sprintf("%s: %s → %s", k, quoteEmpty(va), quoteEmpty(vb)))
		}
	}
	seen = map[string]bool{}
	for _, k := range append(append([]string{}, a.PadOrder...), b.PadOrder...) {
		if seen[k] {
			continue
		}
		seen[k] = true
		na, okA := a.Pads[k]
		nb, okB := b.Pads[k]
		switch {
		case okA && !okB:
			details = append(details, fmt.Sprintf("Pad %s removed (net %s)", k, quoteEmpty(na)))
		case !okA && okB:
			details = append(details, fmt.Sprintf("Pad %s added (net %s)", k, quoteEmpty(nb)))
		case na != nb:
			details = append(details, fmt.Sprintf("Pad %s net: %s → %s", k, quoteEmpty(na), quoteEmpty(nb)))
		}
	}
	for _, k := range sortedKeys(mergeKeys(a.Flags, b.Flags)) {
		if a.Flags[k] != b.Flags[k] {
			details = append(details, fmt.Sprintf("%s: %s → %s", k, quoteEmpty(a.Flags[k]), quoteEmpty(b.Flags[k])))
		}
	}
	semantic := len(details) > 0
	if !samePos(a.X, b.X) || !samePos(a.Y, b.Y) {
		moved = true
		details = append(details, fmt.Sprintf("Moved %s → %s", fmtPos(a.X, a.Y), fmtPos(b.X, b.Y)))
	}
	if !sameAngle(a.Rot, b.Rot) {
		moved = true
		details = append(details, fmt.Sprintf("Rotation: %s° → %s°", fmtNum(a.Rot), fmtNum(b.Rot)))
	}
	if a.Mirror != b.Mirror {
		moved = true
		details = append(details, fmt.Sprintf("Mirror: %s → %s", quoteEmpty(a.Mirror), quoteEmpty(b.Mirror)))
	}
	if !moved && !semantic && a.FieldPos != b.FieldPos {
		moved = true
		details = append(details, "Field text placement/visibility changed")
	}
	if len(details) == 0 {
		return Change{}, false
	}
	op := "changed"
	if !semantic {
		op = "moved"
	}
	return Change{
		Category: componentCategory(b), Op: op, Title: refTitle(b), Subtitle: componentSubtitle(b),
		Details: details, BoxA: a.box(), BoxB: b.box(), layers: union(a.Layers, b.Layers),
	}, true
}

func diffItems(a, b []*Item) []Change {
	var changes []Change
	byUUID := map[string][]*Item{}
	for _, it := range b {
		if it.UUID != "" {
			byUUID[it.UUID] = append(byUUID[it.UUID], it)
		}
	}
	matchedB := map[*Item]bool{}
	var leftoverA []*Item
	for _, ia := range a {
		// A repeated identifier is ambiguous and must not be paired by uuid.
		cands := byUUID[ia.UUID]
		if len(cands) != 1 || cands[0].Kind != ia.Kind || matchedB[cands[0]] {
			leftoverA = append(leftoverA, ia)
			continue
		}
		ib := cands[0]
		matchedB[ib] = true
		if ia.Content == ib.Content {
			continue
		}
		op, details := "changed", []string{}
		if ia.Text != ib.Text {
			details = append(details, fmt.Sprintf("Text: %q → %q", ia.Text, ib.Text))
		}
		for _, k := range sortedKeys(mergeKeys(ia.Attrs, ib.Attrs)) {
			if ia.Attrs[k] != ib.Attrs[k] {
				details = append(details, fmt.Sprintf("%s: %s → %s", k, quoteEmpty(ia.Attrs[k]), quoteEmpty(ib.Attrs[k])))
			}
		}
		if ia.Geom != ib.Geom {
			details = append(details, "Geometry: "+describeGeom(ia)+" → "+describeGeom(ib))
			if ia.Text == ib.Text && ia.Rest == ib.Rest {
				op = "moved"
			}
		}
		if len(details) == 0 {
			details = append(details, "Style/properties changed")
		}
		boxA, boxB := ia.Box, ib.Box
		changes = append(changes, Change{
			Category: ib.category(), Op: op, Title: ib.title(), Details: details,
			BoxA: &boxA, BoxB: &boxB, layers: union(ia.Layers, ib.Layers),
		})
	}
	// UUID matches take precedence. Queue only the remaining B objects, then
	// consume each candidate once even when many objects have identical content.
	bByContent := map[string][]*Item{}
	for _, it := range b {
		if !matchedB[it] {
			key := it.Kind + "|" + it.Content
			bByContent[key] = append(bByContent[key], it)
		}
	}
	for _, ia := range leftoverA {
		key := ia.Kind + "|" + ia.Content
		if candidates := bByContent[key]; len(candidates) > 0 {
			matchedB[candidates[0]] = true
			bByContent[key] = candidates[1:]
			continue
		}
		box := ia.Box
		changes = append(changes, Change{
			Category: ia.category(), Op: "removed", Title: ia.title(),
			Subtitle: describeGeom(ia), BoxA: &box, layers: ia.Layers,
		})
	}
	for _, ib := range b {
		if !matchedB[ib] {
			box := ib.Box
			changes = append(changes, Change{
				Category: ib.category(), Op: "added", Title: ib.title(),
				Subtitle: describeGeom(ib), BoxB: &box, layers: ib.Layers,
			})
		}
	}
	return changes
}

func describeGeom(it *Item) string {
	if it.Desc != "" {
		return it.Desc
	}
	b := it.Box
	switch it.Kind {
	case "wire", "bus":
		return fmt.Sprintf("%s–%s", fmtPos(b[0], b[1]), fmtPos(b[2], b[3]))
	}
	return fmt.Sprintf("near %s", fmtPos(round1((b[0]+b[2])/2), round1((b[1]+b[3])/2)))
}

// posEpsilon is the tolerance for positions (mm) and rotations (degrees).
const posEpsilon = 1e-6

func samePos(a, b float64) bool { return math.Abs(a-b) <= posEpsilon }

// sameAngle compares two angles in degrees, modulo 360.
func sameAngle(a, b float64) bool {
	d := math.Mod(math.Mod(a-b, 360)+540, 360) - 180
	return math.Abs(d) <= posEpsilon
}

// round1 rounds to one decimal; values too large for int64 are returned as is.
func round1(f float64) float64 {
	if math.Abs(f) > 1e15 {
		return f
	}
	return float64(int64(f*10+0.5*sign(f))) / 10
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}

func itemCategory(kind string) string {
	switch kind {
	case "label", "global_label", "hierarchical_label", "directive_label", "netclass_flag":
		return "label"
	case "wire", "bus", "bus_entry":
		return "wire"
	case "junction", "no_connect":
		return "junction"
	case "text", "text_box", "table":
		return "text"
	case "sheet":
		return "sheet"
	}
	return "graphic"
}

func itemTitle(it *Item) string {
	name := strings.ReplaceAll(it.Kind, "_", " ")
	if it.Text != "" {
		t := strings.Join(strings.Fields(it.Text), " ")
		t = truncateTitle(t)
		return fmt.Sprintf("%s “%s”", name, t)
	}
	return name
}

func componentCategory(c *Component) string {
	if c.Footprint {
		return "footprint"
	}
	if c.IsPower() {
		return "power"
	}
	return "component"
}

func refTitle(c *Component) string {
	if c.IsPower() {
		if v := c.Props["Value"]; v != "" {
			return "power " + v + " (" + c.Ref + ")"
		}
	}
	return c.Ref
}

func componentSubtitle(c *Component) string {
	parts := []string{}
	if v := c.Props["Value"]; v != "" {
		parts = append(parts, v)
	}
	lib := c.LibID
	if _, name, ok := strings.Cut(lib, ":"); ok {
		lib = name
	}
	parts = append(parts, lib)
	return strings.Join(parts, " · ")
}

func quoteEmpty(s string) string {
	if s == "" {
		return "(empty)"
	}
	return s
}

// union returns the elements of a and b without duplicates, in order.
func union(a, b []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range append(append([]string{}, a...), b...) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func mergeKeys(a, b map[string]string) map[string]string {
	m := map[string]string{}
	for k := range a {
		m[k] = ""
	}
	for k := range b {
		m[k] = ""
	}
	return m
}

var opOrder = map[string]int{"removed": 0, "added": 1, "changed": 2, "moved": 3}
var catOrder = map[string]int{"component": 0, "sheet": 1, "label": 2, "wire": 3, "junction": 4, "power": 5, "text": 6, "graphic": 7,
	"footprint": 0, "zone": 2, "track": 3, "via": 4, "net": 8, "board": 9}

func sortChanges(cs []Change) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if catOrder[a.Category] != catOrder[b.Category] {
			return catOrder[a.Category] < catOrder[b.Category]
		}
		if (a.Category == "component" || a.Category == "footprint") && a.Title != b.Title {
			return refLess(a.Title, b.Title)
		}
		if opOrder[a.Op] != opOrder[b.Op] {
			return opOrder[a.Op] < opOrder[b.Op]
		}
		return a.Title < b.Title
	})
}

// refLess sorts designators naturally: R2 < R10.
func refLess(a, b string) bool {
	pa, na := splitRef(a)
	pb, nb := splitRef(b)
	if pa != pb {
		return pa < pb
	}
	if na != nb {
		return na < nb
	}
	return a < b
}

func splitRef(r string) (string, int) {
	i := len(r)
	for i > 0 && r[i-1] >= '0' && r[i-1] <= '9' {
		i--
	}
	n := 0
	fmt.Sscanf(r[i:], "%d", &n)
	return r[:i], n
}

func truncateTitle(s string) string {
	r := []rune(s)
	if len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return s
}
