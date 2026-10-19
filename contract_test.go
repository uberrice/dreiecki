package main

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// readViewerHTML reads the viewer from the package directory.
func readViewerHTML(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("viewer.html")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// jsObjectLiterals returns the "{...}" blocks of a JavaScript declaration.
func jsObjectLiterals(decl string) []string {
	var out []string
	depth, start := 0, -1
	for i, r := range decl {
		switch r {
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				out = append(out, decl[start+1:i])
				start = -1
			}
		}
	}
	return out
}

var jsKeyRe = regexp.MustCompile(`([A-Za-z_$][A-Za-z0-9_$]*)\s*:\s*"`)

// jsObjectKeys returns the keys of a JavaScript object literal.
func jsObjectKeys(literal string) []string {
	var keys []string
	for _, m := range jsKeyRe.FindAllStringSubmatch(literal, -1) {
		keys = append(keys, m[1])
	}
	return keys
}

func containsKey(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// viewerCategories returns the categories the viewer knows for schematic and for PCB reports.
func viewerCategories(t *testing.T) (sch, pcb []string) {
	t.Helper()
	html := readViewerHTML(t)
	i := strings.Index(html, "const CATS =")
	if i < 0 {
		t.Fatal("the CATS map is missing from viewer.html")
	}
	decl := html[i:]
	if j := strings.Index(decl, ";"); j >= 0 {
		decl = decl[:j]
	}
	for _, literal := range jsObjectLiterals(decl) {
		keys := jsObjectKeys(literal)
		switch {
		case containsKey(keys, "footprint"):
			pcb = keys
		case containsKey(keys, "component"):
			sch = keys
		}
	}
	if sch == nil || pcb == nil {
		t.Fatalf("the CATS map of viewer.html cannot be read: %q", decl)
	}
	return sch, pcb
}

// The viewer filters the changes on the categories of the CATS map. A category the viewer does not
// know is filtered away without a chip to bring it back, so every category the diff code produces
// has to be in the map of its report kind.
func TestViewerKnowsEveryCategoryTheDiffCodeProduces(t *testing.T) {
	sch, pcb := viewerCategories(t)
	schCats := map[string]bool{}
	for kind := range itemKinds {
		schCats[itemCategory(kind)] = true
	}
	schCats[componentCategory(&Component{Ref: "R1"})] = true
	schCats[componentCategory(&Component{Ref: "#PWR1"})] = true
	for cat := range schCats {
		if !containsKey(sch, cat) {
			t.Errorf("the viewer has no chip for the %q category of schematic changes", cat)
		}
	}
	pcbCats := map[string]bool{}
	for _, cat := range pcbItemKinds {
		pcbCats[cat] = true
	}
	pcbCats[componentCategory(&Component{Footprint: true})] = true
	// diffBoard reports the nets and the board-level settings.
	for _, c := range diffBoard(board(t), board(t, "{TITLE}", "Board v2", "{NETS}", `(net 0 "") (net 1 "GND") (net 2 "VCC") (net 3 "VCC3")`)) {
		pcbCats[c.Category] = true
	}
	for cat := range pcbCats {
		if !containsKey(pcb, cat) {
			t.Errorf("the viewer has no chip for the %q category of PCB changes", cat)
		}
	}
}

// The viewer shows the operation of a change as a symbol.
func TestViewerKnowsEveryOperationTheDiffCodeProduces(t *testing.T) {
	html := readViewerHTML(t)
	i := strings.Index(html, "const OPS =")
	if i < 0 {
		t.Fatal("the OPS map is missing from viewer.html")
	}
	decl := html[i:]
	if j := strings.Index(decl, ";"); j >= 0 {
		decl = decl[:j]
	}
	keys := jsObjectKeys(decl)
	for _, op := range []string{"added", "removed", "changed", "moved"} {
		if !containsKey(keys, op) {
			t.Errorf("the viewer has no symbol for the %q operation", op)
		}
	}
	ops := diffOperations(t)
	for _, op := range []string{"added", "removed", "changed", "moved"} {
		if !containsKey(ops, op) {
			t.Errorf("the fixture does not produce a %q change: %v", op, ops)
		}
	}
	for _, op := range ops {
		if !containsKey(keys, op) {
			t.Errorf("the viewer has no symbol for the %q operation", op)
		}
	}
}

// diffOperations returns the operations of a change set that covers all of them.
func diffOperations(t *testing.T) []string {
	t.Helper()
	const a = `(wire (pts (xy 10 20) (xy 30 40)) (uuid "w1"))
	(label "L1" (at 1 1 0) (uuid "c1"))
	(label "GONE" (at 2 2 0) (uuid "gone"))`
	const b = `(wire (pts (xy 11 20) (xy 30 40)) (uuid "w1"))
	(label "L2" (at 1 1 0) (uuid "c1"))
	(label "NEW" (at 3 3 0) (uuid "new"))`
	_, ca := schematic(t, a)
	_, cb := schematic(t, b)
	var ops []string
	for _, c := range diffItems(ca.Items, cb.Items) {
		ops = append(ops, c.Op)
	}
	return ops
}

// The viewer draws the boxes of the selected change and shows its subtitle.
func TestChangeBoxesAndSubtitlesArePopulated(t *testing.T) {
	const symbol = `(symbol (uuid part) (lib_id "Device:R") (unit 1) (at 10 10 0)
	  (property "Reference" "R1") (property "Value" "10k"))`
	_, a := schematic(t, symbol)
	_, b := schematic(t, strings.ReplaceAll(symbol, "10k", "22k"))
	changes := diffComponents(a.Components, b.Components)
	if len(changes) != 1 {
		t.Fatalf("changes: %+v", changes)
	}
	ch := changes[0]
	if ch.BoxA == nil || ch.BoxB == nil {
		t.Errorf("a component change needs both boxes: %+v", ch)
	}
	if ch.Subtitle == "" {
		t.Errorf("a component change needs a subtitle: %+v", ch)
	}

	const items = `(wire (pts (xy 10 20) (xy 30 40)) (uuid "w1"))
	(label "GONE" (at 2 2 0) (uuid "gone"))`
	const moved = `(wire (pts (xy 11 20) (xy 30 40)) (uuid "w1"))
	(label "NEW" (at 3 3 0) (uuid "new"))`
	_, ia := schematic(t, items)
	_, ib := schematic(t, moved)
	byOp := map[string]Change{}
	for _, c := range diffItems(ia.Items, ib.Items) {
		byOp[c.Op] = c
	}
	for _, op := range []string{"moved", "removed", "added"} {
		c, ok := byOp[op]
		if !ok {
			t.Fatalf("no %q change: %+v", op, byOp)
		}
		if c.BoxA == nil && c.BoxB == nil {
			t.Errorf("a %q change needs a box: %+v", op, c)
		}
		if op != "moved" && c.Subtitle == "" {
			t.Errorf("a %q change needs a subtitle: %+v", op, c)
		}
	}
	if c := byOp["removed"]; c.BoxA == nil {
		t.Errorf("a removed change needs the box of the old side: %+v", c)
	}
	if c := byOp["added"]; c.BoxB == nil {
		t.Errorf("an added change needs the box of the new side: %+v", c)
	}
}

// The names the viewer reads must be the names the report writes.
func TestChangeFieldNamesMatchTheViewer(t *testing.T) {
	typ := reflect.TypeOf(Change{})
	for field, want := range map[string]string{
		"Category": "category",
		"Op":       "op",
		"Title":    "title",
		"Subtitle": "subtitle",
		"Details":  "details",
		"BoxA":     "boxA",
		"BoxB":     "boxB",
	} {
		f, ok := typ.FieldByName(field)
		if !ok {
			t.Errorf("Change has no field %q", field)
			continue
		}
		if got := strings.Split(f.Tag.Get("json"), ",")[0]; got != want {
			t.Errorf("Change.%s is %q in the report; want %q", field, got, want)
		}
	}
	html := readViewerHTML(t)
	for _, name := range []string{"category", "op", "title", "subtitle", "details", "boxA", "boxB"} {
		// A name that is only the start of a longer identifier is not a read of the field.
		if !regexp.MustCompile(`\bc\.` + name + `\b`).MatchString(html) {
			t.Errorf("the viewer never reads c.%s", name)
		}
	}
}

// Extract documents that it may be called from several goroutines on the same project: the
// library geometry cache is guarded by a mutex. The race detector finds the unsynchronised
// map of the cache when the guard is missing.
func TestExtractIsSafeForConcurrentUse(t *testing.T) {
	const body = `(lib_symbols (symbol "Device:R" (symbol "R_0_1" (rectangle (start -1 -2) (end 1 2)))))
	  (symbol (uuid part) (lib_id "Device:R") (unit 1) (at 10 20 0) (property "Reference" "R1"))`
	node, err := ParseSexpr(`(kicad_sch (uuid root) ` + body + `)`)
	if err != nil {
		t.Fatal(err)
	}
	p := &Project{Parsed: map[string]*Node{"test.kicad_sch": node}}
	inst := &SheetInst{UUIDPath: "/root", File: "test.kicad_sch"}
	// The cache is still empty, so the goroutines build it at the same time.
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				sc := Extract(p, inst)
				if len(sc.Components) != 1 || sc.Components[0].LibDef == "" {
					t.Errorf("components: %+v", sc.Components)
					return
				}
			}
		}()
	}
	wg.Wait()
}
