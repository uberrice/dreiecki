package main

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// assertBox compares two boxes with a tolerance, so that the padding arithmetic does not have to
// be spelled out exactly.
func assertItemBox(t *testing.T, name string, got, want Box) {
	t.Helper()
	for i := range want {
		if math.Abs(got[i]-want[i]) > 1e-6 {
			t.Errorf("%s: box %v; want %v", name, got, want)
			return
		}
	}
}

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

func TestItemCategory(t *testing.T) {
	for kind, want := range map[string]string{
		"label": "label", "global_label": "label", "hierarchical_label": "label",
		"directive_label": "label", "netclass_flag": "label",
		"wire": "wire", "bus": "wire", "bus_entry": "wire",
		"junction": "junction", "no_connect": "junction",
		"text": "text", "text_box": "text", "table": "text",
		"sheet":    "sheet",
		"polyline": "graphic", "rectangle": "graphic", "circle": "graphic", "arc": "graphic",
		"bezier": "graphic", "image": "graphic", "rule_area": "graphic",
	} {
		if got := itemCategory(kind); got != want {
			t.Errorf("itemCategory(%q) = %q; want %q", kind, got, want)
		}
	}
	// Every kind the schematic extractor knows must map to a category the viewer can filter on.
	for kind := range itemKinds {
		if itemCategory(kind) == "" {
			t.Errorf("itemCategory(%q) is empty", kind)
		}
	}
}

func TestRefLess(t *testing.T) {
	if !refLess("R2", "R10") {
		t.Error("R2 must sort before R10")
	}
	if refLess("R10", "R2") {
		t.Error("R10 must not sort before R2")
	}
	if !refLess("C1", "R1") {
		t.Error("C1 must sort before R1")
	}
	if refLess("R1", "R1") {
		t.Error("a reference must not sort before itself")
	}
	for _, tc := range []struct {
		ref  string
		part string
		num  int
	}{
		{"R10", "R", 10},
		{"R2", "R", 2},
		{"R", "R", 0},
		{"", "", 0},
		{"10", "", 10},
	} {
		part, num := splitRef(tc.ref)
		if part != tc.part || num != tc.num {
			t.Errorf("splitRef(%q) = %q, %d; want %q, %d", tc.ref, part, num, tc.part, tc.num)
		}
	}
}

func TestSortChanges(t *testing.T) {
	changes := []Change{
		{Category: "wire", Op: "changed", Title: "wire"},
		{Category: "component", Op: "changed", Title: "R10"},
		{Category: "component", Op: "changed", Title: "R2"},
		{Category: "label", Op: "added", Title: "label"},
		{Category: "component", Op: "removed", Title: "R1"},
		{Category: "wire", Op: "removed", Title: "wire"},
	}
	sortChanges(changes)
	var got []string
	for _, c := range changes {
		got = append(got, c.Category+"/"+c.Op+"/"+c.Title)
	}
	want := "component/removed/R1,component/changed/R2,component/changed/R10,label/added/label,wire/removed/wire,wire/changed/wire"
	if strings.Join(got, ",") != want {
		t.Errorf("sorted changes:\n%s\nwant:\n%s", strings.Join(got, ","), want)
	}
}

func TestExtractItem(t *testing.T) {
	const body = `(wire (pts (xy 10 20) (xy 30 40)) (uuid "w1"))
	(label "NET1" (at 50 60 0) (uuid "l1"))
	(junction (at 15 25 0) (uuid "j1"))
	(text_box "Hello" (at 5 5) (size 20 10) (uuid "t1"))`
	_, content := schematic(t, body)
	if len(content.Items) != 4 {
		t.Fatalf("items: %+v", content.Items)
	}
	for _, tc := range []struct {
		kind, text, category string
		box                  Box
	}{
		{"wire", "", "wire", Box{10, 20, 30, 40}},
		{"label", "NET1", "label", Box{47.2, 57.2, 52.8, 62.8}},
		{"junction", "", "junction", Box{13.5, 23.5, 16.5, 26.5}},
		{"text_box", "Hello", "text", Box{1.5, 1.5, 28.5, 18.5}},
	} {
		var it *Item
		for _, candidate := range content.Items {
			if candidate.Kind == tc.kind {
				it = candidate
			}
		}
		if it == nil {
			t.Fatalf("no %s in %+v", tc.kind, content.Items)
		}
		if it.Text != tc.text {
			t.Errorf("%s: text %q; want %q", tc.kind, it.Text, tc.text)
		}
		if got := it.category(); got != tc.category {
			t.Errorf("%s: category %q; want %q", tc.kind, got, tc.category)
		}
		assertItemBox(t, tc.kind, it.Box, tc.box)
	}
}

func TestMovedWireIsReported(t *testing.T) {
	const body = `(wire (pts (xy 10 20) (xy 30 40)) (uuid "w1"))`
	_, a := schematic(t, body)
	_, b := schematic(t, strings.ReplaceAll(body, "(xy 10 20)", "(xy 11 20)"))
	changes := diffItems(a.Items, b.Items)
	if len(changes) != 1 {
		t.Fatalf("changes: %+v", changes)
	}
	ch := changes[0]
	if ch.Op != "moved" {
		t.Errorf("op %q; want %q", ch.Op, "moved")
	}
	if ch.Category != "wire" {
		t.Errorf("category %q; want %q", ch.Category, "wire")
	}
	if len(ch.Details) != 1 || !strings.HasPrefix(ch.Details[0], "Geometry: ") {
		t.Fatalf("details %q; want one %q detail", ch.Details, "Geometry: ")
	}
	// The detail names the old and the new position of the wire.
	for _, want := range []string{"(10, 20)", "(11, 20)"} {
		if !strings.Contains(ch.Details[0], want) {
			t.Errorf("details %q; want them to mention %s", ch.Details[0], want)
		}
	}
	if ch.BoxA == nil || ch.BoxB == nil {
		t.Errorf("missing boxes: %+v", ch)
	}
}

func TestCompareComponentReportsFlagsPadsAndMirror(t *testing.T) {
	const symbol = `(symbol (uuid part) (lib_id "Device:R") (unit 1) (at 10 10 0)
	  (property "Reference" "R1") (property "Value" "10k") %s)`
	for _, tc := range []struct {
		name, old, new string
		details        string
	}{
		{"dnp", "", `(dnp yes)`, "dnp: (empty) → yes"},
		{"in_bom", `(in_bom yes)`, `(in_bom no)`, "in_bom: yes → no"},
		{"mirror", "", `(mirror x)`, "Mirror: (empty) → x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, a := schematic(t, strings.ReplaceAll(symbol, "%s", tc.old))
			_, b := schematic(t, strings.ReplaceAll(symbol, "%s", tc.new))
			ch := diffComponents(a.Components, b.Components)
			if len(ch) != 1 {
				t.Fatalf("changes: %+v", ch)
			}
			if got := strings.Join(ch[0].Details, ";"); got != tc.details {
				t.Errorf("details %q; want %q", got, tc.details)
			}
		})
	}
	// A footprint pad that moves to another net.
	load := func(net string) *Board {
		b, err := LoadBoard(memSource{"b.kicad_pcb": `(kicad_pcb (layers (0 "F.Cu" signal))
  (footprint "Resistor_SMD:R_0603" (layer "F.Cu") (uuid "fp1") (at 50 50)
    (property "Reference" "R1")
    (pad "1" smd rect (at 0 0) (size 1 1) (layers "F.Cu") (net 1 "` + net + `"))))`}, "b.kicad_pcb")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a, b := load("VCC"), load("GND")
	ch := diffComponents(a.Extract().Footprints, b.Extract().Footprints)
	if len(ch) != 1 {
		t.Fatalf("changes: %+v", ch)
	}
	if got := strings.Join(ch[0].Details, ";"); got != "Pad 1 net: VCC → GND" {
		t.Errorf("details %q; want %q", got, "Pad 1 net: VCC → GND")
	}
	if ch[0].Category != "footprint" {
		t.Errorf("category %q; want %q", ch[0].Category, "footprint")
	}
}

func TestLoadAuxCopiesTheProjectFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "hw"), 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"hw/root.kicad_sch": `(kicad_sch (uuid root))`,
		"hw/root.kicad_pro": `{"schematic": {"page_layout_descr_file": "${KIPRJMOD}/layout.wks"}}`,
		"hw/layout.wks":     `(worksheet)`,
		"hw/sym-lib-table":  `(sym_lib_table)`,
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := LoadProject(&workSource{root: dir}, "hw/root.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hw/root.kicad_sch", "hw/root.kicad_pro", "hw/layout.wks", "hw/sym-lib-table"} {
		if _, ok := p.Files[want]; !ok {
			t.Errorf("%s was not copied; files: %v", want, p.Files)
		}
	}
	// Without a project file there is nothing else to copy.
	p, err = LoadProject(memSource{"hw/root.kicad_sch": `(kicad_sch (uuid root))`}, "hw/root.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 {
		t.Errorf("files: %v", p.Files)
	}
}
