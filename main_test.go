package main

import (
	"flag"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	for _, tc := range []struct {
		args []string
		pos  []string
		out  string
		pcb  bool
	}{
		{[]string{"HEAD~1"}, []string{"HEAD~1"}, "", false},
		{[]string{"-pcb", "-o", "x.html", "HEAD~1"}, []string{"HEAD~1"}, "x.html", true},
		{[]string{"v7..v8", "-o", "review.html", "--pcb"}, []string{"v7..v8"}, "review.html", true},
		{[]string{"main", "-pcb", "feature"}, []string{"main", "feature"}, "", true},
		{[]string{"-o", "x.html", "--", "-weird", "-pcb"}, []string{"-weird", "-pcb"}, "x.html", false},
		{nil, nil, "", false},
	} {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		out := fs.String("o", "", "")
		pcb := fs.Bool("pcb", false, "")
		pos := parseArgs(fs, tc.args)
		if !reflect.DeepEqual(pos, tc.pos) || *out != tc.out || *pcb != tc.pcb {
			t.Errorf("%q: got %q -o %q -pcb %v, want %q -o %q -pcb %v", tc.args, pos, *out, *pcb, tc.pos, tc.out, tc.pcb)
		}
	}
}

func TestParseRefs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		a, b string
		err  bool
	}{
		{"view mode", nil, "", "", false},
		{"one revision", []string{"HEAD~1"}, "HEAD~1", worktreeRef, false},
		{"range", []string{"A..B"}, "A", "B", false},
		{"version range", []string{"v1.0.0..v2.0.0"}, "v1.0.0", "v2.0.0", false},
		{"two revisions", []string{"A", "B"}, "A", "B", false},
		{"three dots", []string{"A...B"}, "", "", true},
		{"empty revision", []string{""}, "", "", true},
		{"too many revisions", []string{"A", "B", "C"}, "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, b, err := parseRefs(tc.args)
			if tc.err {
				if err == nil {
					t.Fatalf("parseRefs(%q) = %q, %q; want an error", tc.args, a, b)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRefs(%q): %v", tc.args, err)
			}
			if a != tc.a || b != tc.b {
				t.Errorf("parseRefs(%q) = %q, %q; want %q, %q", tc.args, a, b, tc.a, tc.b)
			}
		})
	}
}

// slugOf calls slug and turns a panic into a failed test, so that one bad case does not abort the
// whole run.
func slugOf(t *testing.T, r RevInfo) string {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			t.Errorf("slug(%+v) panicked: %v", r, p)
		}
	}()
	return slug(r)
}

func TestSlug(t *testing.T) {
	const commit = "abcdef1234567890abcdef1234567890abcdef12"
	for _, tc := range []struct {
		name string
		info RevInfo
		want string
	}{
		{"worktree", RevInfo{Label: "working tree"}, "worktree"},
		{"short label", RevInfo{Label: "main", Commit: commit}, "main"},
		{"punctuation", RevInfo{Label: "feature/new thing", Commit: commit}, "feature_new_thing"},
		{"no label", RevInfo{Commit: commit}, "abcdef12"},
		{"long label", RevInfo{Label: strings.Repeat("x", 60), Commit: commit}, "abcdef12"},
		// The commit prefix must not be cut short of the commit's own length.
		{"short commit", RevInfo{Label: strings.Repeat("x", 60), Commit: "abc"}, "abc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := slugOf(t, tc.info); got != tc.want {
				t.Errorf("slug(%+v) = %q; want %q", tc.info, got, tc.want)
			}
		})
	}
}

func TestStripVolatileRemovesTheTitle(t *testing.T) {
	// The title carries the export path, which differs between two exports of the same sheet.
	if got := stripVolatile([]byte("<svg><title>a/b.kicad_sch</title>x</svg>")); string(got) != "<svg>x</svg>" {
		t.Errorf("stripVolatile = %q; want %q", got, "<svg>x</svg>")
	}
	if got := stripVolatile([]byte("<svg>x</svg>")); string(got) != "<svg>x</svg>" {
		t.Errorf("stripVolatile = %q; want it unchanged", got)
	}
}

// A sheet instance in a fixture project.
type childSheet struct {
	uuid, name, file string
}

// rootSheetFixture builds a root schematic with the given child sheets.
func rootSheetFixture(children ...childSheet) string {
	var b strings.Builder
	b.WriteString(`(kicad_sch (uuid root)`)
	for _, c := range children {
		fmt.Fprintf(&b, ` (sheet (at 10 10) (size 20 20) (uuid %q) (property "Sheetname" %q) (property "Sheetfile" %q))`, c.uuid, c.name, c.file)
	}
	b.WriteString(` (sheet_instances (path "/" (page "1"))))`)
	return b.String()
}

// childSheetFixture builds a child schematic holding one wire.
func childSheetFixture(uuid string) string {
	return `(kicad_sch (uuid ` + uuid + `) (wire (pts (xy 10 20) (xy 30 40)) (uuid "w1")))`
}

// sheetFixtureRenders returns a render for every sheet of a project.
func sheetFixtureRenders(p *Project) map[string]*RenderedPage {
	renders := map[string]*RenderedPage{}
	for _, s := range p.Sheets {
		renders[s.Key()] = &RenderedPage{SVG: []byte("<svg/>"), Width: 297, Height: 210}
	}
	return renders
}

// pageStatuses maps the page names of a report to their status.
func pageStatuses(rep *Report) map[string]string {
	status := map[string]string{}
	for _, p := range rep.Pages {
		status[p.Name] = p.Status
	}
	return status
}

func TestBuildReportPairsSheets(t *testing.T) {
	load := func(t *testing.T, src memSource) *Project {
		t.Helper()
		p, err := LoadProject(src, "root.kicad_sch")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// A sheet renamed in B: same uuid, different name.
	t.Run("renamed", func(t *testing.T) {
		a := load(t, memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s1", "Sub", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")})
		b := load(t, memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s1", "Renamed", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")})
		rep := buildReport(a, b, sheetFixtureRenders(a), sheetFixtureRenders(b))
		if len(rep.Pages) != 2 {
			t.Fatalf("pages: %+v", rep.Pages)
		}
		status := pageStatuses(rep)
		// The renamed sheet keeps its uuid, so it is paired with the old one.
		if status["/Renamed"] != "same" {
			t.Errorf("renamed sheet: %q; want %q", status["/Renamed"], "same")
		}
		if status["/"] != "changed" {
			t.Errorf("root sheet: %q; want %q", status["/"], "changed")
		}
	})
	// A sheet that exists only in B.
	t.Run("added", func(t *testing.T) {
		a := load(t, memSource{"root.kicad_sch": rootSheetFixture()})
		b := load(t, memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s1", "Sub", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")})
		rep := buildReport(a, b, sheetFixtureRenders(a), sheetFixtureRenders(b))
		if len(rep.Pages) != 2 {
			t.Fatalf("pages: %+v", rep.Pages)
		}
		status := pageStatuses(rep)
		if status["/Sub"] != "added" {
			t.Errorf("added sheet: %q; want %q", status["/Sub"], "added")
		}
		if status["/"] != "changed" {
			t.Errorf("root sheet: %q; want %q", status["/"], "changed")
		}
	})
	// A sheet that exists only in A.
	t.Run("removed", func(t *testing.T) {
		a := load(t, memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s1", "Sub", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")})
		b := load(t, memSource{"root.kicad_sch": rootSheetFixture()})
		rep := buildReport(a, b, sheetFixtureRenders(a), sheetFixtureRenders(b))
		if len(rep.Pages) != 2 {
			t.Fatalf("pages: %+v", rep.Pages)
		}
		status := pageStatuses(rep)
		if status["/Sub"] != "removed" {
			t.Errorf("removed sheet: %q; want %q", status["/Sub"], "removed")
		}
		if status["/"] != "changed" {
			t.Errorf("root sheet: %q; want %q", status["/"], "changed")
		}
	})
	// A sheet whose parent changed: neither the uuid path nor the display name matches, so it is
	// reported as removed and added rather than as the same sheet.
	t.Run("moved to another parent", func(t *testing.T) {
		a := load(t, memSource{
			"root.kicad_sch": rootSheetFixture(childSheet{"p1", "P1", "p1.kicad_sch"}),
			"p1.kicad_sch":   `(kicad_sch (uuid p1) (sheet (at 10 10) (size 20 20) (uuid "s1") (property "Sheetname" "Sub") (property "Sheetfile" "sub.kicad_sch")))`,
			"sub.kicad_sch":  childSheetFixture("sub"),
		})
		b := load(t, memSource{
			"root.kicad_sch": rootSheetFixture(childSheet{"p2", "P2", "p2.kicad_sch"}),
			"p2.kicad_sch":   `(kicad_sch (uuid p2) (sheet (at 10 10) (size 20 20) (uuid "s1") (property "Sheetname" "Sub") (property "Sheetfile" "sub.kicad_sch")))`,
			"sub.kicad_sch":  childSheetFixture("sub"),
		})
		rep := buildReport(a, b, sheetFixtureRenders(a), sheetFixtureRenders(b))
		status := pageStatuses(rep)
		if status["/P1/Sub"] != "removed" || status["/P2/Sub"] != "added" {
			t.Errorf("moved sheet: %v", status)
		}
		if status["/P1"] != "removed" || status["/P2"] != "added" {
			t.Errorf("moved parent: %v", status)
		}
	})
	// A sheet recreated with a new uuid but the same name: paired by name.
	t.Run("recreated", func(t *testing.T) {
		a := load(t, memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s1", "Sub", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")})
		b := load(t, memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s2", "Sub", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")})
		rep := buildReport(a, b, sheetFixtureRenders(a), sheetFixtureRenders(b))
		if len(rep.Pages) != 2 {
			t.Fatalf("pages: %+v", rep.Pages)
		}
		status := pageStatuses(rep)
		if status["/Sub"] != "same" {
			t.Errorf("recreated sheet: %q; want %q", status["/Sub"], "same")
		}
	})
}

func TestBuildReportIgnoresTheVolatileTitle(t *testing.T) {
	src := memSource{"root.kicad_sch": rootSheetFixture(childSheet{"s1", "Sub", "sub.kicad_sch"}), "sub.kicad_sch": childSheetFixture("sub")}
	load := func(t *testing.T) *Project {
		t.Helper()
		p, err := LoadProject(src, "root.kicad_sch")
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	a, b := load(t), load(t)
	ra, rb := sheetFixtureRenders(a), sheetFixtureRenders(b)
	// The two revisions render the same sheet, but KiCad writes the export path into the title.
	for key := range ra {
		ra[key] = &RenderedPage{SVG: []byte(`<svg><title>/tmp/a/root.kicad_sch</title>body</svg>`), Width: 297, Height: 210}
		rb[key] = &RenderedPage{SVG: []byte(`<svg><title>/tmp/b/root.kicad_sch</title>body</svg>`), Width: 297, Height: 210}
	}
	rep := buildReport(a, b, ra, rb)
	for _, p := range rep.Pages {
		if p.Status != "same" {
			t.Errorf("page %s: %q; want %q", p.Name, p.Status, "same")
		}
		if len(p.Changes) != 0 {
			t.Errorf("page %s: %+v", p.Name, p.Changes)
		}
	}
}
