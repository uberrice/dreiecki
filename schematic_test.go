package main

import (
	"strings"
	"testing"
)

func schematic(t *testing.T, body string) (*Project, *SheetContent) {
	t.Helper()
	p, err := LoadProject(memSource{"test.kicad_sch": `(kicad_sch (uuid root) ` + body + `)`}, "test.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	return p, Extract(p, p.Sheets[0])
}

func TestSchematicHierarchyAndInstanceReferences(t *testing.T) {
	src := memSource{
		"hw/root.kicad_sch": `(kicad_sch (uuid root)
   (sheet (uuid first) (property "Sheetname" "First") (property "Sheetfile" "sub/shared.kicad_sch")
    (instances (project "root" (path "/root" (page "10")))))
   (sheet (uuid second) (property "Sheet name" "Second") (property "Sheet file" "sub/shared.kicad_sch")
    (instances (project "root" (path "/root" (page "2")))))
   (sheet_instances (path "/" (page "1"))))`,
		"hw/sub/shared.kicad_sch": `(kicad_sch (uuid child)
   (symbol (lib_id "Device:R") (uuid part) (unit 1) (property "Reference" "R?")
    (property "Value" "10k")
    (instances (project "root" (path "/root/first" (reference "R10") (unit 1))
     (path "/root/second" (reference "R2") (unit 2))))))`,
	}
	p, err := LoadProject(src, "hw/root.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Sheets) != 3 || len(p.Parsed) != 2 || len(p.Files) != 2 {
		t.Fatalf("unexpected hierarchy: %+v", p)
	}
	for i, want := range []struct{ path, name, page, ref, unit string }{
		{"/root", "/", "1", "", ""},
		{"/root/second", "/Second", "2", "R2", "2"},
		{"/root/first", "/First", "10", "R10", "1"},
	} {
		sheet := p.Sheets[i]
		if sheet.Key() != want.path || sheet.DisplayName() != want.name || sheet.Page != want.page {
			t.Errorf("sheet %d: %+v", i, sheet)
		}
		if i > 0 {
			c := Extract(p, sheet).Components[0]
			if c.Ref != want.ref || c.Unit != want.unit {
				t.Errorf("instance %s: %+v", sheet.Key(), c)
			}
		}
	}
}

func TestComponentsWithoutUUIDs(t *testing.T) {
	const body = `(symbol (lib_id "Device:R") (unit 1) (property "Reference" "R1") (property "Value" "10k"))
  (symbol (lib_id "Device:R") (unit 1) (property "Reference" "R2") (property "Value" "20k"))`
	_, a := schematic(t, body)
	_, b := schematic(t, body)
	if ch := diffComponents(a.Components, b.Components); len(ch) != 0 {
		t.Fatalf("identical UUID-less components: %+v", ch)
	}
	_, b = schematic(t, strings.ReplaceAll(body, `"20k"`, `"22k"`))
	if ch := diffComponents(a.Components, b.Components); len(ch) != 1 || ch[0].Title != "R2" || ch[0].Op != "changed" {
		t.Fatalf("reference matching: %+v", ch)
	}
}
