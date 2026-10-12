package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindRootInDottedDeepDirectory(t *testing.T) {
	repo := t.TempDir()
	const rel = "hw-v1.2/a/b/c/d/e/f/g/µ-controller.kicad_pro"
	file := filepath.Join(repo, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "init", "-q"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"); err != nil {
		t.Fatal(err)
	}
	src, err := newGitSource(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []Source{src, &workSource{root: repo}} {
		for _, ext := range []string{".kicad_sch", ".kicad_pcb"} {
			got, err := findRootFile(repo, repo, "", ext, source)
			want := "hw-v1.2/a/b/c/d/e/f/g/µ-controller" + ext
			if err != nil || got != want {
				t.Fatalf("%T: got %q, %v; want %q", source, got, err, want)
			}
		}
	}
}

func TestFindRootRejectsMultipleProjects(t *testing.T) {
	src := memSource{"hw-v1.2/a.kicad_pro": "{}", "hw-v2.0/b.kicad_pro": "{}"}
	repo := t.TempDir()
	if _, err := findRootFile(repo, repo, "", ".kicad_sch", src); err == nil {
		t.Fatal("multiple projects must require explicit selection")
	}
}
