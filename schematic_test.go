package main

import (
	"strings"
	"testing"
	"unicode/utf8"
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

func TestSchematicLegacyPageNumbers(t *testing.T) {
	src := memSource{
		"root.kicad_sch": `(kicad_sch (uuid root)
   (sheet (uuid a) (property "Sheetname" "A") (property "Sheetfile" "child.kicad_sch"))
   (sheet (uuid b) (property "Sheetname" "B") (property "Sheetfile" "child.kicad_sch"))
   (sheet_instances (path "/" (page "1")) (path "/a" (page "10"))
    (path "/b" (page "2")) (path "/b/nested" (page "3")) (path "/a/nested" (page "11"))))`,
		"child.kicad_sch": `(kicad_sch (uuid child) (sheet (uuid nested)
   (property "Sheetname" "Nested") (property "Sheetfile" "leaf.kicad_sch")))`,
		"leaf.kicad_sch": `(kicad_sch (uuid leaf))`,
	}
	p, err := LoadProject(src, "root.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	var pages []string
	for _, s := range p.Sheets {
		pages = append(pages, s.Key()+"="+s.Page)
	}
	if got := strings.Join(pages, ","); got != "/root=1,/root/b=2,/root/b/nested=3,/root/a=10,/root/a/nested=11" {
		t.Fatal(got)
	}
}

func TestSchematicLibraryMetadataAndGeometry(t *testing.T) {
	const body = `(lib_symbols (symbol "Device:R"
  (property "Description" "old") (property "ki_keywords" "resistor")
  (symbol "R_0_1" (rectangle (start -1 -2) (end 1 2)))
  (symbol "R_1_1" (pin passive line (at 0 3 90) (length 1) (name "~") (number "1")))))
  (symbol (uuid part) (lib_id "Device:R") (unit 1) (property "Reference" "R1"))`
	_, a := schematic(t, body)
	for _, tc := range []struct {
		name, old, new string
		changed        bool
	}{
		{"description", `"old"`, `"new"`, false},
		{"keywords", `"resistor"`, `"passive"`, false},
		{"pin", "(length 1)", "(length 2)", true},
		{"graphic", "(end 1 2)", "(end 2 2)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, b := schematic(t, strings.ReplaceAll(body, tc.old, tc.new))
			ch := diffComponents(a.Components, b.Components)
			if !tc.changed && len(ch) != 0 {
				t.Fatalf("metadata reported as geometry: %+v", ch)
			}
			if tc.changed && (len(ch) != 1 || strings.Join(ch[0].Details, ";") != "Library symbol definition updated (pins/graphics)") {
				t.Fatalf("missing geometry change: %+v", ch)
			}
		})
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

func TestUTF8Titles(t *testing.T) {
	for _, suffix := range []string{"µ", "Ω", "ä"} {
		for _, count := range []int{0, 39, 40, 41} {
			text := strings.Repeat("x", count) + suffix
			want := text
			if count >= 40 {
				want = strings.Repeat("x", 40) + "…"
			}
			title := itemTitle(&Item{Kind: "label", Text: text})
			if !utf8.ValidString(title) || title != "label “"+want+"”" {
				t.Errorf("schematic title: %q", title)
			}
			n, err := ParseSexpr(`(gr_text "` + text + `" (at 0 0) (layer "F.SilkS"))`)
			if err != nil {
				t.Fatal(err)
			}
			title = board(t).item(n).Title
			if !utf8.ValidString(title) || title != "text “"+want+"” (F.Silkscreen)" {
				t.Errorf("PCB title: %q", title)
			}
		}
	}
}

// KiCad 8 stores (hide yes) inside effects, as in its video demo:
// https://github.com/KiCad/kicad-source-mirror/blob/8.0/demos/video/video.kicad_sch
func TestSchematicFieldVisibility(t *testing.T) {
	const body = `(symbol (uuid part) (lib_id "Device:R") (unit 1)
  (property "Reference" "R1" (at 10 10 0))
  (property "Value" "10k" (at 10 12 0) %s))`
	_, a := schematic(t, strings.ReplaceAll(body, "%s", `(effects (font (size 1 1)))`))
	for _, hidden := range []string{`(hide yes)`, `(hide)`, `hide`, `(effects (font (size 1 1)) hide)`, `(effects (font (size 1 1)) (hide yes))`} {
		_, b := schematic(t, strings.ReplaceAll(body, "%s", hidden))
		if ch := diffComponents(a.Components, b.Components); len(ch) != 1 || ch[0].Op != "moved" || strings.Join(ch[0].Details, ";") != "Field text placement/visibility changed" {
			t.Errorf("%s: %+v", hidden, ch)
		}
	}
	_, b := schematic(t, strings.ReplaceAll(body, "%s", `(effects (font (size 1 1)) (hide no))`))
	if ch := diffComponents(a.Components, b.Components); len(ch) != 0 {
		t.Fatal(ch)
	}
}
