package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeCLI(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI requires a POSIX shell")
	}
	file := filepath.Join(t.TempDir(), "kicad-cli")
	script := `#!/bin/sh
set -eu
while [ "$#" -gt 0 ]; do
 if [ "$1" = "-o" ]; then out="$2"; shift; fi
 shift
done
` + body
	if err := os.WriteFile(file, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestRenderMatchesFilenamesWithoutEnglishOutput(t *testing.T) {
	cli := fakeCLI(t, `printf '%s' '<svg viewBox="0 0 297 210">root</svg>' > "$out/test.svg"
printf '%s' '<svg viewBox="0 0 297 210">second</svg>' > "$out/test-B.svg"
printf '%s' '<svg viewBox="0 0 297 210">first</svg>' > "$out/test-A_B_C.svg"
printf '%s\n' "Geplottet nach 'test-B.svg'" "Geplottet nach 'test-A_B_C.svg'"
`)
	p := &Project{Name: "test", Root: "test.kicad_sch", Files: map[string][]byte{"test.kicad_sch": []byte("unused")}, Sheets: []*SheetInst{
		{UUIDPath: "/root", Page: "1"},
		{UUIDPath: "/root/first", Names: []string{`A/B\C`}, Page: "2"},
		{UUIDPath: "/root/second", Names: []string{"B"}, Page: "3"},
	}}
	pages, err := Render(p, t.TempDir(), RenderOptions{KicadCLI: cli})
	if err != nil {
		t.Fatal(err)
	}
	for i, content := range []string{"root", "first", "second"} {
		page := pages[p.Sheets[i].Key()]
		if page == nil || !strings.Contains(string(page.SVG), ">"+content+"</svg>") {
			t.Fatalf("wrong sheet %d: %+v", i, page)
		}
	}
}

func TestRenderRejectsMissingOrAmbiguousSheets(t *testing.T) {
	for _, tc := range []struct {
		name   string
		sheets []*SheetInst
		files  []string
		want   string
	}{
		{"missing", []*SheetInst{{Names: []string{"A"}}}, []string{"test-unrelated.svg"}, "no SVG matching"},
		{"sanitization collision", []*SheetInst{{Names: []string{"A/B"}}, {Names: []string{"A_B"}}}, []string{"test-A_B.svg"}, "ambiguous SVG"},
		{"hierarchy collision", []*SheetInst{{Names: []string{"A-B"}}, {Names: []string{"A", "B"}}}, []string{"test-A-B.svg"}, "ambiguous SVG"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, file := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, file), []byte("svg"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			_, err := matchSheetSVGs(&Project{Name: "test", Sheets: tc.sheets}, dir)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
		})
	}
}

// This smoke test also checks KiCad's actual filename sanitization when installed.
func TestRenderWithKiCad(t *testing.T) {
	cli, err := exec.LookPath("kicad-cli")
	if err != nil {
		t.Skip("kicad-cli is not installed")
	}
	const rootUUID = "00000000-0000-0000-0000-000000000001"
	const sheetUUID = "00000000-0000-0000-0000-000000000002"
	src := memSource{
		"test.kicad_sch": `(kicad_sch (version 20230121) (generator eeschema)
   (uuid "` + rootUUID + `") (paper "A4") (lib_symbols)
   (sheet (at 10 10) (size 20 20) (stroke (width 0) (type default)) (fill (color 0 0 0 0))
    (uuid "` + sheetUUID + `")
    (property "Sheetname" "A/B\\C" (at 10 9 0) (effects (font (size 1.27 1.27))))
    (property "Sheetfile" "child.kicad_sch" (at 10 31 0) (effects (font (size 1.27 1.27))))
    (instances (project "test" (path "/` + rootUUID + `" (page "2")))))
   (sheet_instances (path "/" (page "1"))))`,
		"child.kicad_sch": `(kicad_sch (version 20230121) (generator eeschema)
   (uuid "00000000-0000-0000-0000-000000000003") (paper "A4") (lib_symbols)
   (text "child-render-marker" (at 20 20 0) (effects (font (size 1.27 1.27)))
    (uuid "00000000-0000-0000-0000-000000000004")))`,
	}
	p, err := LoadProject(src, "test.kicad_sch")
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := Render(p, t.TempDir(), RenderOptions{KicadCLI: cli, ExcludeDrawSheet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rendered) != 2 {
		t.Fatalf("got %d pages", len(rendered))
	}
	child := rendered["/"+rootUUID+"/"+sheetUUID]
	if child == nil || !strings.Contains(string(child.SVG), "child-render-marker") {
		t.Fatal("child SVG missing or mapped to another sheet")
	}
}
