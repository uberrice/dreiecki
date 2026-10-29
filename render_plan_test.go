package main

import (
	"reflect"
	"testing"
)

func TestResolvePCBPages(t *testing.T) {
	b := &Board{Layers: []string{"F.Cu", "B.Cu", "Edge.Cuts"}}
	pages := []pcbPage{
		{ID: "absent", Layer: "In1.Cu", Layers: []string{"In1.Cu", "Edge.Cuts"}},
		{ID: "overview", Stack: true, Layers: []string{"B.Cu", "In1.Cu", "F.Cu", "Edge.Cuts"}},
		{ID: "front", Layer: "F.Cu", Layers: []string{"F.Cu", "Edge.Cuts"}},
		{ID: "empty", Stack: true, Layers: []string{"F.SilkS"}},
	}
	got := resolvePCBPages(b, pages)
	if len(got) != 3 {
		t.Fatalf("want overview, front and empty pages, got %+v", got)
	}
	for i, want := range []struct {
		id     string
		index  int
		stack  bool
		layers []string
	}{
		{"overview", 1, true, []string{"B.Cu", "F.Cu", "Edge.Cuts"}},
		{"front", 2, false, []string{"F.Cu", "Edge.Cuts"}},
		{"empty", 3, true, nil},
	} {
		if pg := got[i]; pg.ID != want.id || pg.index != want.index || pg.Stack != want.stack || !reflect.DeepEqual(pg.layers, want.layers) {
			t.Errorf("page %d: got %+v, want %+v", i, pg, want)
		}
	}
	// A page plan is shared by both revisions. Filtering and editing the resolved
	// layers must not remove layers from the other revision's request.
	got[0].layers[0] = "changed"
	if want := []string{"B.Cu", "In1.Cu", "F.Cu", "Edge.Cuts"}; !reflect.DeepEqual(pages[1].Layers, want) {
		t.Errorf("shared page plan mutated: %v", pages[1].Layers)
	}
	back := resolvePCBPages(&Board{Layers: []string{"In1.Cu", "Edge.Cuts"}}, pages)
	if len(back) != 3 || back[0].ID != "absent" || !reflect.DeepEqual(back[1].layers, []string{"In1.Cu", "Edge.Cuts"}) {
		t.Errorf("second revision did not retain its own pages and layers: %+v", back)
	}
}
