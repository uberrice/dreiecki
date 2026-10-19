package main

import (
	"fmt"
	"strings"
	"testing"
)

// A legacy file has no uuids, so repeated power symbols can only be paired by
// their content: two identical symbols are no change at all, not a removal
// and an addition.
func TestLegacyDuplicateReferenceIsNoChange(t *testing.T) {
	gnd := func(x int) string {
		return fmt.Sprintf(`(symbol (lib_id "power:GND") (at %d 0 0) (property "Reference" "#GND") (property "Value" "GND"))`, x)
	}
	_, a := schematic(t, gnd(10)+gnd(30))
	_, b := schematic(t, gnd(10)+gnd(30))
	if ch := diffComponents(a.Components, b.Components); len(ch) != 0 {
		t.Fatalf("identical legacy symbols reported: %+v", ch)
	}
	// The order of the symbols does not matter.
	_, c := schematic(t, gnd(30)+gnd(10))
	if ch := diffComponents(c.Components, b.Components); len(ch) != 0 {
		t.Fatalf("identical legacy symbols reported: %+v", ch)
	}
}

// A duplicated reference whose parts really differ must still be reported.
func TestLegacyDuplicateReferenceReportsRealChange(t *testing.T) {
	gnd := func(x int, value string) string {
		return fmt.Sprintf(`(symbol (lib_id "power:GND") (at %d 0 0) (property "Reference" "#GND") (property "Value" "%s"))`, x, value)
	}
	_, a := schematic(t, gnd(10, "GND")+gnd(30, "GND"))
	_, b := schematic(t, gnd(10, "GND")+gnd(30, "AGND"))
	ch := diffComponents(a.Components, b.Components)
	if len(ch) != 1 || ch[0].Op != "changed" ||
		!strings.Contains(strings.Join(ch[0].Details, ";"), "Value: GND → AGND") {
		t.Fatalf("changed value not reported: %+v", ch)
	}
	_, c := schematic(t, gnd(10, "GND")+gnd(31, "GND"))
	ch = diffComponents(a.Components, c.Components)
	if len(ch) != 1 || ch[0].Op != "moved" {
		t.Fatalf("moved symbol not reported: %+v", ch)
	}
}

// Two parts that share a reference are paired by their content. A part whose
// flag or whose mirror changed did change, so it is not the same part: the
// report must not depend on the order of the parts.
func TestAmbiguousReferenceFlagChangeIsOrderIndependent(t *testing.T) {
	sym := func(uuid, extra string) string {
		return fmt.Sprintf(`(symbol (uuid %s) (lib_id "Device:R") (unit 1) (at 10 20 0) (property "Reference" "R1") (property "Value" "10k") %s)`, uuid, extra)
	}
	// A file holds the two parts. The uuids differ from file to file, so that
	// the parts are paired by their reference and not by their uuid.
	file := func(tag, first, second string) string {
		return sym(tag+"1", first) + sym(tag+"2", second)
	}
	for _, tc := range []struct{ name, extra, detail string }{
		{"flag", `(dnp yes)`, "dnp: (empty) → yes"},
		{"mirror", `(mirror x)`, "Mirror: (empty) → x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The same two parts, in the same order and in the opposite one.
			_, a := schematic(t, file("a", "", tc.extra))
			_, b := schematic(t, file("b", "", tc.extra))
			_, c := schematic(t, file("c", tc.extra, ""))
			if ch := diffComponents(a.Components, b.Components); len(ch) != 0 {
				t.Fatalf("the same parts reported a change: %+v", ch)
			}
			if ch := diffComponents(c.Components, b.Components); len(ch) != 0 {
				t.Fatalf("the report depends on the order of the parts: %+v", ch)
			}
			// A part whose flag or whose mirror really changed is reported.
			_, d := schematic(t, file("d", tc.extra, tc.extra))
			ch := diffComponents(a.Components, d.Components)
			if len(ch) != 1 || !strings.Contains(strings.Join(ch[0].Details, ";"), tc.detail) {
				t.Fatalf("the changed part not reported: %+v", ch)
			}
		})
	}
}
