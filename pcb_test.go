package main

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
)

// memSource serves files from memory.
type memSource map[string]string

func (m memSource) ReadFile(rel string) ([]byte, error) {
	if s, ok := m[rel]; ok {
		return []byte(s), nil
	}
	return nil, fs.ErrNotExist
}
func (m memSource) ListDir(string) ([]string, error) { return nil, nil }
func (m memSource) ListFiles() ([]string, error) {
	var files []string
	for file := range m {
		files = append(files, file)
	}
	sort.Strings(files)
	return files, nil
}
func (m memSource) Info() RevInfo { return RevInfo{} }

// A KiCad 8 style board; {NETS}, {GND}, {AT} and {VIA} are substituted per test.
const boardTmpl = `(kicad_pcb (version 20240108) (generator "pcbnew")
  (layers (0 "F.Cu" signal) (31 "B.Cu" signal) (37 "F.SilkS" user "F.Silkscreen") (44 "Edge.Cuts" user))
  {NETS}
  (title_block (title "{TITLE}"))
  (footprint "Resistor_SMD:R_0603" (layer "F.Cu") (uuid "fp1") (at {AT})
    (property "Reference" "R1" (at 0 -1.4 0) (layer "F.SilkS") (uuid "p1"))
    (property "Value" "10k" (at 0 1.4 0) (layer "F.Fab") (uuid "p2"))
    (pad "1" smd roundrect (at -0.8 0 {PADROT}) (size 0.8 0.9) (layers "F.Cu" "F.Paste" "F.Mask") (net {GND} "GND") (uuid "pad1"))
    (pad "2" smd roundrect (at 0.8 0 {PADROT}) (size 0.8 0.9) (layers "F.Cu" "F.Paste" "F.Mask") (net {VCC} "VCC") (uuid "pad2"))
  )
  (segment (start {SEG}) (end 20 10) (width 0.25) (layer "F.Cu") (net {GND}) (uuid "s1"))
  (via (at {VIA}) (size 0.6) (drill 0.3) (layers "F.Cu" "B.Cu") (net {VCC}) (uuid "v1"))
)`

func board(t *testing.T, repl ...string) *Board {
	t.Helper()
	r := map[string]string{
		"{NETS}": `(net 0 "") (net 1 "GND") (net 2 "VCC")`, "{GND}": "1", "{VCC}": "2",
		"{AT}": "50 50", "{PADROT}": "0", "{VIA}": "30 30", "{SEG}": "10 10", "{TITLE}": "Board",
	}
	for i := 0; i < len(repl); i += 2 {
		r[repl[i]] = repl[i+1]
	}
	s := boardTmpl
	for k, v := range r {
		s = strings.ReplaceAll(s, k, v)
	}
	b, err := LoadBoard(memSource{"b.kicad_pcb": s}, "b.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func boardChanges(a, b *Board) []Change {
	ca, cb := a.Extract(), b.Extract()
	return append(append(diffComponents(ca.Footprints, cb.Footprints), diffItems(ca.Items, cb.Items)...), diffBoard(a, b)...)
}

func TestPCBNetRenumberIsNotAChange(t *testing.T) {
	a := board(t)
	b := board(t, "{NETS}", `(net 0 "") (net 1 "AAA") (net 2 "GND") (net 3 "VCC")`, "{GND}", "2", "{VCC}", "3")
	ch := boardChanges(a, b)
	if len(ch) != 1 || ch[0].Category != "net" || ch[0].Title != "AAA" || ch[0].Op != "added" {
		t.Fatalf("want only net AAA added, got %+v", ch)
	}
}

func TestPCBFootprintRotateAndFields(t *testing.T) {
	a := board(t)
	b := board(t, "{AT}", "50 50 90", "{PADROT}", "90")
	ch := boardChanges(a, b)
	if len(ch) != 1 || ch[0].Op != "moved" || ch[0].Title != "R1" {
		t.Fatalf("want R1 moved, got %+v", ch)
	}
	if strings.Join(ch[0].Details, ";") != "Rotation: 0° → 90°" {
		t.Errorf("details: %q", ch[0].Details)
	}
	fp := a.Extract().Footprints[0]
	if fp.Props["Value"] != "10k" || fp.Pads["2"] != "VCC" {
		t.Errorf("fields/pads not read: %+v %+v", fp.Props, fp.Pads)
	}
}

func TestPCBViaLayersAndPages(t *testing.T) {
	a := board(t)
	b := board(t, "{VIA}", "31 30")
	ch := boardChanges(a, b)
	if len(ch) != 1 || ch[0].Category != "via" || strings.Join(ch[0].layers, ",") != "F.Cu,B.Cu" {
		t.Fatalf("want via move on both copper layers, got %+v", ch)
	}
	var names []string
	for _, p := range planPages(a, b) {
		names = append(names, p.Name)
	}
	// F.Mask etc. are used by the pads but missing from the layer table; Edge.Cuts is empty.
	if got := strings.Join(names, ","); got != "All layers,F.Cu,B.Cu,F.Silkscreen" {
		t.Errorf("pages: %s", got)
	}
}

func TestPCBMetadataIsNotGeometry(t *testing.T) {
	const body = `(kicad_pcb (layers (0 "F.Cu" signal))
  (footprint "Test" (layer "F.Cu") (uuid fp) (at 1 1)
   (property "Reference" "U1")
   (pad "1" smd rect (at 0 0) (size 1 1) (layers "F.Cu")
    (pinfunction "IN") (pintype "input"))
   (model "old.step" (offset (xyz 0 0 0)))))`
	load := func(s string) *Board {
		b, err := LoadBoard(memSource{"b.kicad_pcb": s}, "b.kicad_pcb")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	a := load(body)
	for _, tc := range []struct {
		old, new string
		changed  bool
	}{
		{`"old.step"`, `"new.step"`, false},
		{`(xyz 0 0 0)`, `(xyz 1 0 0)`, false},
		{`"IN"`, `"OUT"`, false},
		{`"input"`, `"output"`, false},
		{`(size 1 1)`, `(size 2 1)`, true},
	} {
		ch := boardChanges(a, load(strings.ReplaceAll(body, tc.old, tc.new)))
		if !tc.changed && len(ch) != 0 {
			t.Errorf("%s: %+v", tc.old, ch)
		}
		if tc.changed && (len(ch) != 1 || strings.Join(ch[0].Details, ";") != "Footprint geometry changed (pads/graphics)") {
			t.Errorf("geometry change: %+v", ch)
		}
	}
}

func TestPCBPlotSettingsAreNotSetup(t *testing.T) {
	load := func(width, format string) *Board {
		b, err := LoadBoard(memSource{"b.kicad_pcb": `(kicad_pcb (setup (pad_to_mask_clearance ` + width + `) (pcbplotparams (outputformat ` + format + `))))`}, "b.kicad_pcb")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	if ch := diffBoard(load("0", "1"), load("0", "2")); len(ch) != 0 {
		t.Fatal(ch)
	}
	if ch := diffBoard(load("0", "1"), load("0.1", "1")); len(ch) != 1 || ch[0].Title != "Board setup / stackup" {
		t.Fatal(ch)
	}
}

func TestDiffBoardReportsTheTitleBlock(t *testing.T) {
	load := func(title string) *Board {
		b, err := LoadBoard(memSource{"b.kicad_pcb": `(kicad_pcb (title_block (title "` + title + `") (date "2024-01-01") (rev "1")))`}, "b.kicad_pcb")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	ch := diffBoard(load("Board"), load("Board v2"))
	if len(ch) != 1 {
		t.Fatalf("changes: %+v", ch)
	}
	if ch[0].Category != "board" || ch[0].Op != "changed" || ch[0].Title != "Title block" {
		t.Errorf("change: %+v", ch[0])
	}
	if got := strings.Join(ch[0].Details, ";"); got != "title: Board → Board v2" {
		t.Errorf("details %q; want %q", got, "title: Board → Board v2")
	}
	// An unchanged title block is not a change.
	if ch := diffBoard(load("Board"), load("Board")); len(ch) != 0 {
		t.Errorf("changes: %+v", ch)
	}
}

func TestExpandLayerWildcards(t *testing.T) {
	b, err := LoadBoard(memSource{"b.kicad_pcb": `(kicad_pcb (layers
  (0 "F.Cu" signal) (1 "In1.Cu" signal) (2 "In2.Cu" signal) (31 "B.Cu" signal)
  (39 "F.Mask" user) (40 "B.Mask" user)))`}, "b.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		arg  string
		want string
	}{
		{"*.Cu", "F.Cu,In1.Cu,In2.Cu,B.Cu"},
		{"*In.Cu", "In1.Cu,In2.Cu"},
		{"*.Mask", "F.Mask,B.Mask"},
		{"F&B.Cu", "F.Cu,B.Cu"},
		{"F.Cu", "F.Cu"},
	} {
		n, err := ParseSexpr(`(layer "` + tc.arg + `")`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Join(b.expand(n), ","); got != tc.want {
			t.Errorf("expand(%q) = %q; want %q", tc.arg, got, tc.want)
		}
	}
}

func TestStackDiffers(t *testing.T) {
	if stackDiffers(nil) {
		t.Error("an empty stack does not differ")
	}
	if stackDiffers([]StackLayer{{Layer: "F.Cu", SvgA: "a", SvgB: "a"}}) {
		t.Error("identical layers do not differ")
	}
	if !stackDiffers([]StackLayer{{Layer: "F.Cu", SvgA: "a", SvgB: "a"}, {Layer: "B.Cu", SvgA: "b", SvgB: "c"}}) {
		t.Error("a differing layer must be reported")
	}
}

func TestPCBChangesReachOnlyTheirLayer(t *testing.T) {
	a := board(t)
	b := board(t, "{SEG}", "10 12") // the track on F.Cu moved
	pages := planPages(a, b)
	renders := func() map[string]*RenderedPage {
		m := map[string]*RenderedPage{}
		for _, pg := range pages {
			m[pg.ID] = &RenderedPage{SVG: []byte("<svg/>"), Width: 100, Height: 80}
		}
		return m
	}
	rep := buildPCBReport(a, b, pages, renders(), renders())
	byName := map[string]PageReport{}
	for _, p := range rep.Pages {
		byName[p.Name] = p
	}
	for _, name := range []string{"All layers", "F.Cu"} {
		page, ok := byName[name]
		if !ok {
			t.Fatalf("no page %q; pages: %+v", name, rep.Pages)
		}
		if len(page.Changes) != 1 {
			t.Fatalf("page %q: %+v", name, page.Changes)
		}
		if page.Changes[0].Category != "track" {
			t.Errorf("page %q: category %q; want %q", name, page.Changes[0].Category, "track")
		}
	}
	for _, name := range []string{"B.Cu", "F.Silkscreen"} {
		page, ok := byName[name]
		if !ok {
			t.Fatalf("no page %q; pages: %+v", name, rep.Pages)
		}
		if len(page.Changes) != 0 {
			t.Errorf("page %q: %+v", name, page.Changes)
		}
	}
}

func TestStackedPageStatusFollowsItsLayers(t *testing.T) {
	a, b := board(t), board(t)
	pages := []pcbPage{{ID: "all", Name: "All layers", Stack: true, Layers: []string{"F.Cu", "B.Cu"}}}
	renders := func(bcu string) map[string]*RenderedPage {
		return map[string]*RenderedPage{"all": {SVG: []byte("<svg/>"), Width: 100, Height: 80, Layers: []*LayerSVG{
			{Layer: "F.Cu", SVG: []byte("<svg><title>a.kicad_pcb</title>x</svg>")},
			{Layer: "B.Cu", SVG: []byte(bcu)},
		}}}
	}
	rep := buildPCBReport(a, b, pages, renders("<svg>a</svg>"), renders("<svg>b</svg>"))
	if len(rep.Pages) != 1 || rep.Pages[0].Status != "changed" {
		t.Fatalf("pages: %+v", rep.Pages)
	}
	rep = buildPCBReport(a, b, pages, renders("<svg>a</svg>"), renders("<svg>a</svg>"))
	if len(rep.Pages) != 1 || rep.Pages[0].Status != "same" {
		t.Fatalf("pages: %+v", rep.Pages)
	}
}
