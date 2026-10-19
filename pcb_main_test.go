package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func loadPCBFixture(t *testing.T, src string) *Board {
	t.Helper()
	b, err := LoadBoard(memSource{"b.kicad_pcb": src}, "b.kicad_pcb")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// unnamedPadBoard is a footprint with n pads that carry no number. The first and
// the third pad have the same geometry but different nets, so that the net
// tiebreak of the pad order matters.
func unnamedPadBoard(n int, reverse bool) string {
	pads := []string{
		`    (pad "" smd rect (at -1 0) (size 1 1) (layers "F.Cu") (net 1 "GND") (uuid "pad1"))`,
		`    (pad "" smd rect (at 1 0) (size 1 1) (layers "F.Cu") (net 2 "VCC") (uuid "pad2"))`,
		`    (pad "" smd rect (at -1 0) (size 1 1) (layers "F.Cu") (net 3 "SIG") (uuid "pad3"))`,
	}
	sel := pads[:n]
	if reverse {
		sel = append([]string{}, sel...)
		for i, j := 0, len(sel)-1; i < j; i, j = i+1, j-1 {
			sel[i], sel[j] = sel[j], sel[i]
		}
	}
	s := `(kicad_pcb (version 20240108)
  (layers (0 "F.Cu" signal) (37 "F.SilkS" user "F.Silkscreen"))
  (net 1 "GND") (net 2 "VCC") (net 3 "SIG")
  (footprint "Test:TwoPads" (layer "F.Cu") (uuid "fp1") (at 50 50)
    (property "Reference" "U1" (at 0 -1 0) (layer "F.SilkS") (uuid "p1"))
`
	for _, p := range sel {
		s += p + "\n"
	}
	return s + "  )\n)\n"
}

func TestPCBTextExtentsUseRunes(t *testing.T) {
	const label = "日本語です" // 5 runes, 15 bytes
	b := loadPCBFixture(t, `(kicad_pcb (version 20240108)
  (layers (0 "F.Cu" signal) (37 "F.SilkS" user "F.Silkscreen"))
  (gr_text "`+label+`" (at 10 20) (layer "F.SilkS") (uuid "t1"))
)`)
	items := b.Extract().Items
	if len(items) != 1 {
		t.Fatalf("want one item, got %d", len(items))
	}
	it := items[0]
	if it.Category != "text" || it.Text != label {
		t.Fatalf("item = %q %q, want a text item %q", it.Category, it.Text, label)
	}
	// Five characters at 0.5 mm on both sides of the anchor.
	want := Box{7.5, 17.5, 12.5, 22.5}
	if it.Box != want {
		t.Errorf("box = %v, want %v (the extent must count runes, not bytes)", it.Box, want)
	}
}

func TestPCBUnnamedPadKeys(t *testing.T) {
	b := loadPCBFixture(t, unnamedPadBoard(2, false))
	fp := b.Extract().Footprints[0]
	if len(fp.Pads) != 2 {
		t.Fatalf("want two pads, got %v", fp.Pads)
	}
	if got := fp.Pads["(unnamed)"]; got != "GND" {
		t.Errorf("pad (unnamed) = %q, want GND", got)
	}
	if got := fp.Pads["(unnamed 2)"]; got != "VCC" {
		t.Errorf("pad (unnamed 2) = %q, want VCC", got)
	}
	if got := strings.Join(fp.PadOrder, ","); got != "(unnamed),(unnamed 2)" {
		t.Errorf("pad order = %q, want (unnamed),(unnamed 2)", got)
	}
}

func TestPCBUnnamedPadAdded(t *testing.T) {
	a := loadPCBFixture(t, unnamedPadBoard(1, false))
	b := loadPCBFixture(t, unnamedPadBoard(2, false))
	ch := diffComponents(a.Extract().Footprints, b.Extract().Footprints)
	want := "Pad (unnamed 2) added (net VCC)"
	found := false
	for _, c := range ch {
		for _, d := range c.Details {
			if d == want {
				found = true
			}
			if strings.HasPrefix(d, "Pad ") && !strings.HasPrefix(d, "Pad (unnamed") {
				t.Errorf("unnamed pad reported as %q", d)
			}
		}
	}
	if !found {
		t.Errorf("want the detail %q, got %+v", want, ch)
	}
}

func TestPCBUnnamedPadReorderIsNoNetChange(t *testing.T) {
	// Two pads share their geometry, so only the net tells them apart.
	a := loadPCBFixture(t, unnamedPadBoard(3, false))
	b := loadPCBFixture(t, unnamedPadBoard(3, true))
	ch := diffComponents(a.Extract().Footprints, b.Extract().Footprints)
	for _, c := range ch {
		for _, d := range c.Details {
			if strings.HasPrefix(d, "Pad ") {
				t.Errorf("reordering unnamed pads reported a pad change: %q", d)
			}
		}
	}
}

func TestParseRefsRejectsEmptyRevision(t *testing.T) {
	for _, args := range [][]string{{"", "HEAD"}, {"HEAD", ""}} {
		_, _, err := parseRefs(args)
		if err == nil {
			t.Errorf("parseRefs(%q): want an error", args)
		} else if !strings.Contains(err.Error(), "empty") {
			t.Errorf("parseRefs(%q): error %q should mention the empty revision", args, err)
		}
	}
}

func TestOutputPathMissingDirectory(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-dir", "report.html")
	err := checkOutputPath(missing)
	if err == nil {
		t.Fatal("want an error for a missing output directory")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("error %q should say that the output directory is missing", err)
	}
	if err := checkOutputPath(filepath.Join(t.TempDir(), "report.html")); err != nil {
		t.Errorf("a writable directory should be accepted: %v", err)
	}
}

// The output location is checked before anything is rendered.
func TestRunChecksTheOutputPath(t *testing.T) {
	t.Chdir(t.TempDir())
	missing := filepath.Join(t.TempDir(), "no-such-dir", "report.html")
	err := run("HEAD", "HEAD", "", missing, false, false, false, RenderOptions{KicadCLI: "/bin/true"})
	if err == nil {
		t.Fatal("want an error for a missing output directory")
	}
	if !strings.Contains(err.Error(), "no-such-dir") {
		t.Errorf("error %q should name the missing output directory", err)
	}
}

// The browser command must not go through a shell.
func TestBrowserCmdPerPlatform(t *testing.T) {
	for _, tc := range []struct {
		goos string
		want string
	}{
		{"linux", "xdg-open report.html"},
		{"darwin", "open report.html"},
		{"windows", "rundll32 url.dll,FileProtocolHandler report.html"},
	} {
		cmd := browserCmd(tc.goos, "report.html")
		if got := strings.Join(cmd.Args, " "); got != tc.want {
			t.Errorf("browserCmd(%q) = %q; want %q", tc.goos, got, tc.want)
		}
	}
	// On Windows the path must not go through cmd.exe, whose separators (&, %)
	// would be acted on.
	if args := strings.Join(browserCmd("windows", "report.html").Args, " "); strings.Contains(args, "cmd") {
		t.Errorf("the Windows browser command goes through cmd: %q", args)
	}
}

func TestOpenCmdReportsStartFailure(t *testing.T) {
	err := openCmd(exec.Command(filepath.Join(t.TempDir(), "no-such-browser")), "report.html")
	if err == nil {
		t.Fatal("want an error when the browser cannot be started")
	}
	if !strings.Contains(err.Error(), "could not open") {
		t.Errorf("error %q should say that the report could not be opened", err)
	}
}

// The board renderer plots the pages of the two revisions in parallel.
func TestComparePCBRendersBothRevisions(t *testing.T) {
	cli := fakeCLI(t, `printf '%s' '<svg viewBox="0 0 100 80"></svg>' > "$out"`)
	board := func(net, name string) memSource {
		return memSource{"test.kicad_pcb": `(kicad_pcb (version 20240108) (layers (0 "F.Cu" signal))
		  (net 1 "GND") (net 2 "VCC") (net 3 "SIG")
		  (footprint "Test:R" (layer "F.Cu") (uuid "fp1") (at 50 50)
		    (property "Reference" "R1" (at 0 -1 0) (layer "F.SilkS") (uuid "p1"))
		    (pad "1" smd rect (at -1 0) (size 1 1) (layers "F.Cu") (net ` + net + ` "` + name + `") (uuid "pad1"))))`}
	}
	srcA := board("1", "GND")
	srcB := board("3", "SIG")
	rep, err := comparePCB(srcA, &srcB, "test.kicad_pcb", t.TempDir(), RenderOptions{KicadCLI: cli})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Pages) == 0 {
		t.Fatal("the board has no pages")
	}
	found := false
	for _, page := range rep.Pages {
		for _, c := range page.Changes {
			for _, d := range c.Details {
				if d == "Pad 1 net: GND → SIG" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Fatalf("the net change of the pad is missing: %+v", rep.Pages)
	}
}
