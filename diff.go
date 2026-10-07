package main

import (
	"fmt"
	"sort"
	"strings"
)

// Change is one semantic difference shown in the report's change list.
type Change struct {
	Category string   `json:"category"` // component, power, label, wire, junction, text, sheet, graphic, other
	Op       string   `json:"op"`       // added, removed, changed, moved
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Details  []string `json:"details,omitempty"`
	BoxA     *Box     `json:"boxA,omitempty"`
	BoxB     *Box     `json:"boxB,omitempty"`
}

const symbolRadius = 6.0

func diffComponents(a, b []*Component) []Change {
	var changes []Change
	byUUID := map[string]*Component{}
	for _, c := range b {
		byUUID[c.UUID] = c
	}
	matchedB := map[*Component]bool{}
	var unmatchedA []*Component
	pairs := [][2]*Component{}
	for _, ca := range a {
		if cb, ok := byUUID[ca.UUID]; ok && !matchedB[cb] {
			matchedB[cb] = true
			pairs = append(pairs, [2]*Component{ca, cb})
		} else {
			unmatchedA = append(unmatchedA, ca)
		}
	}
	// Fall back to reference designator + unit (e.g. part deleted and re-placed).
	byRef := map[string]*Component{}
	for _, cb := range b {
		if !matchedB[cb] {
			byRef[cb.Ref+"/"+cb.Unit] = cb
		}
	}
	var removed []*Component
	for _, ca := range unmatchedA {
		if cb, ok := byRef[ca.Ref+"/"+ca.Unit]; ok && !matchedB[cb] && !strings.HasSuffix(ca.Ref, "?") {
			matchedB[cb] = true
			pairs = append(pairs, [2]*Component{ca, cb})
		} else {
			removed = append(removed, ca)
		}
	}
	for _, p := range pairs {
		if ch, ok := compareComponent(p[0], p[1]); ok {
			changes = append(changes, ch)
		}
	}
	for _, ca := range removed {
		changes = append(changes, Change{
			Category: componentCategory(ca), Op: "removed", Title: refTitle(ca),
			Subtitle: componentSubtitle(ca), BoxA: pointBox(ca.X, ca.Y, symbolRadius),
		})
	}
	for _, cb := range b {
		if !matchedB[cb] {
			changes = append(changes, Change{
				Category: componentCategory(cb), Op: "added", Title: refTitle(cb),
				Subtitle: componentSubtitle(cb), BoxB: pointBox(cb.X, cb.Y, symbolRadius),
			})
		}
	}
	return changes
}

func compareComponent(a, b *Component) (Change, bool) {
	var details []string
	moved := false
	if a.Ref != b.Ref {
		details = append(details, fmt.Sprintf("Reference: %s → %s", a.Ref, b.Ref))
	}
	if a.LibID != b.LibID {
		details = append(details, fmt.Sprintf("Symbol: %s → %s", a.LibID, b.LibID))
	} else if a.LibDef != b.LibDef && a.LibDef != "" && b.LibDef != "" {
		details = append(details, "Library symbol definition updated (pins/graphics)")
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
	for _, k := range sortedKeys(mergeKeys(a.Flags, b.Flags)) {
		if a.Flags[k] != b.Flags[k] {
			details = append(details, fmt.Sprintf("%s: %s → %s", k, quoteEmpty(a.Flags[k]), quoteEmpty(b.Flags[k])))
		}
	}
	semantic := len(details) > 0
	if a.X != b.X || a.Y != b.Y {
		moved = true
		details = append(details, fmt.Sprintf("Moved %s → %s", fmtPos(a.X, a.Y), fmtPos(b.X, b.Y)))
	}
	if a.Rot != b.Rot {
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
		Details: details, BoxA: pointBox(a.X, a.Y, symbolRadius), BoxB: pointBox(b.X, b.Y, symbolRadius),
	}, true
}

func diffItems(a, b []*Item) []Change {
	var changes []Change
	byUUID := map[string]*Item{}
	for _, it := range b {
		if it.UUID != "" {
			byUUID[it.UUID] = it
		}
	}
	// Items without a matching uuid are matched by identical content, so
	// re-created but identical objects don't show up as noise.
	bByContent := map[string][]*Item{}
	for _, it := range b {
		bByContent[it.Kind+"|"+it.Content] = append(bByContent[it.Kind+"|"+it.Content], it)
	}
	matchedB := map[*Item]bool{}
	var leftoverA []*Item
	for _, ia := range a {
		ib, ok := byUUID[ia.UUID]
		if !ok || ia.UUID == "" || ib.Kind != ia.Kind {
			leftoverA = append(leftoverA, ia)
			continue
		}
		matchedB[ib] = true
		if ia.Content == ib.Content {
			continue
		}
		op, details := "changed", []string{}
		if ia.Text != ib.Text {
			details = append(details, fmt.Sprintf("Text: %q → %q", ia.Text, ib.Text))
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
			Category: itemCategory(ib.Kind), Op: op, Title: itemTitle(ib), Details: details,
			BoxA: &boxA, BoxB: &boxB,
		})
	}
	for _, ia := range leftoverA {
		key := ia.Kind + "|" + ia.Content
		if cands := bByContent[key]; len(cands) > 0 {
			found := false
			for _, ib := range cands {
				if !matchedB[ib] {
					matchedB[ib] = true
					found = true
					break
				}
			}
			if found {
				continue
			}
		}
		box := ia.Box
		changes = append(changes, Change{
			Category: itemCategory(ia.Kind), Op: "removed", Title: itemTitle(ia),
			Subtitle: describeGeom(ia), BoxA: &box,
		})
	}
	for _, ib := range b {
		if !matchedB[ib] {
			box := ib.Box
			changes = append(changes, Change{
				Category: itemCategory(ib.Kind), Op: "added", Title: itemTitle(ib),
				Subtitle: describeGeom(ib), BoxB: &box,
			})
		}
	}
	return changes
}

func describeGeom(it *Item) string {
	b := it.Box
	switch it.Kind {
	case "wire", "bus":
		return fmt.Sprintf("%s–%s", fmtPos(b[0], b[1]), fmtPos(b[2], b[3]))
	}
	return fmt.Sprintf("near %s", fmtPos(round1((b[0]+b[2])/2), round1((b[1]+b[3])/2)))
}

func round1(f float64) float64 { return float64(int64(f*10+0.5*sign(f))) / 10 }

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
		if len(t) > 40 {
			t = t[:40] + "…"
		}
		return fmt.Sprintf("%s “%s”", name, t)
	}
	return name
}

func componentCategory(c *Component) string {
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
var catOrder = map[string]int{"component": 0, "sheet": 1, "label": 2, "wire": 3, "junction": 4, "power": 5, "text": 6, "graphic": 7}

func sortChanges(cs []Change) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if catOrder[a.Category] != catOrder[b.Category] {
			return catOrder[a.Category] < catOrder[b.Category]
		}
		if a.Category == "component" && a.Title != b.Title {
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
