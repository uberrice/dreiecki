package main

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// coreSummary renders changes as comparable strings.
func coreSummary(changes []Change) []string {
	out := []string{}
	for _, c := range changes {
		out = append(out, c.Op+" "+c.Title+" "+strings.Join(c.Details, ";"))
	}
	return out
}

// coreOps renders the changes as "operation title" strings.
func coreOps(changes []Change) []string {
	out := []string{}
	for _, c := range changes {
		out = append(out, c.Op+" "+c.Title)
	}
	return out
}

// coreFind returns the change with the given operation and title.
func coreFind(changes []Change, op, title string) *Change {
	for i, c := range changes {
		if c.Op == op && c.Title == title {
			return &changes[i]
		}
	}
	return nil
}

// dupPart is a part with a repeated identifier, as legacy files have.
func dupPart(ref, value string) string {
	return fmt.Sprintf(`(symbol (uuid dup) (lib_id "Device:R") (unit 1) (property "Reference" "%s") (property "Value" "%s"))`, ref, value)
}

// A repeated identifier is ambiguous: the parts must not be paired by uuid,
// and a file compared with itself must not report any change.
func TestDiffComponentsDuplicateUUID(t *testing.T) {
	for _, tc := range []struct {
		name string
		a, b string
		want string
	}{
		// The two sides disagree, so that a wrong pairing becomes visible.
		{"ambiguous identifier", dupPart("R1", "10k") + dupPart("R2", "10k"),
			dupPart("R9", "10k") + dupPart("R2", "10k"), "added R9,removed R1"},
		// One B part may be matched by at most one A part.
		{"one B part matched twice", dupPart("R1", "10k") + dupPart("R2", "10k"),
			dupPart("R1", "10k"), "removed R2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, a := schematic(t, tc.a)
			_, b := schematic(t, tc.b)
			got := coreOps(diffComponents(a.Components, b.Components))
			sort.Strings(got)
			if strings.Join(got, ",") != tc.want {
				t.Errorf("duplicate uuid: %v; want %s", got, tc.want)
			}
		})
	}
	// A file compared with itself must not report any change.
	_, same := schematic(t, dupPart("R1", "10k")+dupPart("R2", "10k"))
	if ch := diffComponents(same.Components, same.Components); len(ch) != 0 {
		t.Fatalf("identical files with duplicate uuids: %+v", ch)
	}
}

// Legacy files repeat their identifier, so a change must be attributed to the
// part that really changed.
func TestDiffComponentsDuplicateUUIDAttributesChange(t *testing.T) {
	_, a := schematic(t, dupPart("R1", "10k")+dupPart("R2", "20k"))
	_, b := schematic(t, dupPart("R9", "10k")+dupPart("R2", "22k"))
	ch := diffComponents(a.Components, b.Components)
	got := coreOps(ch)
	sort.Strings(got)
	if strings.Join(got, ",") != "added R9,changed R2,removed R1" {
		t.Fatalf("change attributed to the wrong part: %v", got)
	}
	if c := coreFind(ch, "changed", "R2"); c == nil || strings.Join(c.Details, ";") != "Value: 20k → 22k" {
		t.Fatalf("change attributed to the wrong part: %+v", ch)
	}
}

// A B object may be matched by at most one A object; the second A object falls
// through to the content matching.
func TestDiffItemsDoesNotReuseB(t *testing.T) {
	a := []*Item{{UUID: "x", Kind: "wire", Content: "old"}, {UUID: "x", Kind: "wire", Content: "other"}}
	b := []*Item{{UUID: "x", Kind: "wire", Content: "new"}}
	ch := diffItems(a, b)
	ops := []string{}
	for _, c := range ch {
		ops = append(ops, c.Op)
	}
	sort.Strings(ops)
	if strings.Join(ops, ",") != "changed,removed" {
		t.Fatalf("one B object matched twice: %+v", ch)
	}
}

// A repeated identifier is ambiguous, so the items fall back to content matching.
func TestDiffItemsDuplicateUUID(t *testing.T) {
	a := []*Item{{UUID: "dup", Kind: "wire", Content: "same"}}
	// The first item of B has another content, so that pairing by uuid would show a change.
	b := []*Item{{UUID: "dup", Kind: "wire", Content: "other"}, {UUID: "dup", Kind: "wire", Content: "same"}}
	ch := diffItems(a, b)
	if got := coreOps(ch); strings.Join(got, ",") != "added wire" {
		t.Fatalf("duplicate uuid: %v; want one added item and no change", got)
	}
}

// A reference that is not unique must not be paired, and the result must not
// depend on the order of the components.
func TestRefFallbackNeedsUniqueReference(t *testing.T) {
	sym := func(uuid, value string) string {
		return fmt.Sprintf(`(symbol (uuid %s) (lib_id "Device:R") (unit 1) (property "Reference" "R1") (property "Value" "%s"))`, uuid, value)
	}
	_, first := schematic(t, sym("a1", "10k")+sym("a2", "22k"))
	_, second := schematic(t, sym("a2", "22k")+sym("a1", "10k"))
	_, b := schematic(t, sym("b1", "10k"))
	for _, a := range []*SheetContent{first, second} {
		ch := diffComponents(a.Components, b.Components)
		for _, c := range ch {
			if c.Op == "changed" {
				t.Fatalf("ambiguous reference paired: %+v", ch)
			}
		}
	}
	if !reflect.DeepEqual(coreSummary(diffComponents(first.Components, b.Components)),
		coreSummary(diffComponents(second.Components, b.Components))) {
		t.Fatal("reference fallback depends on the order of the components")
	}
	// A unique reference is still used to pair the parts.
	_, c1 := schematic(t, sym("c1", "10k"))
	_, c2 := schematic(t, sym("c2", "22k"))
	ch := diffComponents(c1.Components, c2.Components)
	if len(ch) != 1 || ch[0].Op != "changed" ||
		!strings.Contains(strings.Join(ch[0].Details, ";"), "Value: 10k → 22k") {
		t.Fatalf("unique reference not paired: %+v", ch)
	}
}

// Positions are compared with a tolerance, rotations modulo 360.
func TestPositionAndRotationTolerance(t *testing.T) {
	part := func(x, y, rot float64) *Component {
		return &Component{UUID: "u", Ref: "R1", Unit: "1", LibID: "Device:R", X: x, Y: y, Rot: rot,
			Props: map[string]string{"Value": "10k"}, Flags: map[string]string{}}
	}
	for _, tc := range []struct {
		name string
		a, b *Component
		want bool
	}{
		{"sub-nanometre move", part(1, 2, 0), part(1+1e-7, 2, 0), false},
		{"full turn", part(1, 2, 0), part(1, 2, 360), false},
		{"negative angle", part(1, 2, -90), part(1, 2, 270), false},
		{"real move", part(1, 2, 0), part(1.001, 2, 0), true},
		{"real rotation", part(1, 2, 0), part(1, 2, 90), true},
	} {
		ch := diffComponents([]*Component{tc.a}, []*Component{tc.b})
		if tc.want && len(ch) != 1 {
			t.Errorf("%s: %+v", tc.name, ch)
		}
		if !tc.want && len(ch) != 0 {
			t.Errorf("%s: %+v", tc.name, ch)
		}
	}
}

// A wire is described by its endpoints, not by the corners of its bounding box.
func TestDescribeGeomUsesWireEndpoints(t *testing.T) {
	node, err := ParseSexpr(`(wire (pts (xy 0 0) (xy 5 5) (xy 10 0)) (uuid w)
	  (stroke (width 0) (type default)) (layer "F.Cu"))`)
	if err != nil {
		t.Fatal(err)
	}
	it := extractItem(node)
	if got, want := describeGeom(it), "(0, 0)–(10, 0)"; got != want {
		t.Errorf("wire description %q, want %q", got, want)
	}
	if it.Box != (Box{0, 0, 10, 5}) {
		t.Errorf("wire box %v", it.Box)
	}
	// The description is also the subtitle of added and removed items.
	ch := diffItems(nil, []*Item{it})
	if len(ch) != 1 || ch[0].Op != "added" || ch[0].Subtitle != "(0, 0)–(10, 0)" {
		t.Errorf("added wire: %+v", ch)
	}
}

// Text extents are measured in characters, not in bytes.
func TestItemBoxExtentUsesRunes(t *testing.T) {
	const text = "电阻电阻"
	node, err := ParseSexpr(`(label "` + text + `" (at 0 0))`)
	if err != nil {
		t.Fatal(err)
	}
	it := extractItem(node)
	// Four characters at 0.7 mm on both sides of the anchor.
	if got := it.Box[2] - it.Box[0]; math.Abs(got-5.6) > 1e-9 {
		t.Errorf("label width %v, want 5.6 (the extent must count characters, not bytes)", got)
	}
}

// The library geometry of a file is built once, not once per sheet instance.
func TestLibraryGeometryIsBuiltOncePerFile(t *testing.T) {
	const body = `(lib_symbols (symbol "Device:R" (symbol "R_0_1" (rectangle (start -1 -2) (end 1 2)))))
	  (symbol (uuid part) (lib_id "Device:R") (unit 1) (property "Reference" "R1"))`
	p, sc := schematic(t, body)
	want := map[string]string{}
	for _, s := range p.Parsed["test.kicad_sch"].Child("lib_symbols").Children("symbol") {
		want[s.Arg(0)] = symbolGeometry(s)
	}
	if sc.Components[0].LibDef != want["Device:R"] {
		t.Fatalf("library geometry %q", sc.Components[0].LibDef)
	}
	// The geometry is cached in the project, per file.
	if got := p.libGeom["test.kicad_sch"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("cached geometry %v, want %v", got, want)
	}
	// A second instance of the same file is served from the cache.
	p.Sheets = append(p.Sheets, &SheetInst{UUIDPath: "/root/second", File: "test.kicad_sch", Page: "2"})
	Extract(p, p.Sheets[1])
	if len(p.libGeom) != 1 {
		t.Fatalf("library geometry built for %d files", len(p.libGeom))
	}
}

// Coordinates that are not finite numbers must not reach the report.
func TestNumNeverReturnsNonFinite(t *testing.T) {
	for _, s := range []string{"NaN", "Inf", "-Inf", "inf", "abc", "1e999", "", " 1"} {
		f := num(s)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			t.Errorf("num(%q) = %v", s, f)
		}
	}
	if num("12.5") != 12.5 || num("abc") != 0 || num("NaN") != 0 {
		t.Error("unexpected value")
	}
}
