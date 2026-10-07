package main

import (
	"io/fs"
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
func (m memSource) Info() RevInfo                    { return RevInfo{} }

// A KiCad 8 style board; {NETS}, {GND}, {AT} and {VIA} are substituted per test.
const boardTmpl = `(kicad_pcb (version 20240108) (generator "pcbnew")
  (layers (0 "F.Cu" signal) (31 "B.Cu" signal) (37 "F.SilkS" user "F.Silkscreen") (44 "Edge.Cuts" user))
  {NETS}
  (footprint "Resistor_SMD:R_0603" (layer "F.Cu") (uuid "fp1") (at {AT})
    (property "Reference" "R1" (at 0 -1.4 0) (layer "F.SilkS") (uuid "p1"))
    (property "Value" "10k" (at 0 1.4 0) (layer "F.Fab") (uuid "p2"))
    (pad "1" smd roundrect (at -0.8 0 {PADROT}) (size 0.8 0.9) (layers "F.Cu" "F.Paste" "F.Mask") (net {GND} "GND") (uuid "pad1"))
    (pad "2" smd roundrect (at 0.8 0 {PADROT}) (size 0.8 0.9) (layers "F.Cu" "F.Paste" "F.Mask") (net {VCC} "VCC") (uuid "pad2"))
  )
  (segment (start 10 10) (end 20 10) (width 0.25) (layer "F.Cu") (net {GND}) (uuid "s1"))
  (via (at {VIA}) (size 0.6) (drill 0.3) (layers "F.Cu" "B.Cu") (net {VCC}) (uuid "v1"))
)`

func board(t *testing.T, repl ...string) *Board {
	t.Helper()
	r := map[string]string{
		"{NETS}": `(net 0 "") (net 1 "GND") (net 2 "VCC")`, "{GND}": "1", "{VCC}": "2",
		"{AT}": "50 50", "{PADROT}": "0", "{VIA}": "30 30",
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
