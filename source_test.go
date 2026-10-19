package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	defer src.Close()
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

func TestGitSourceBatchReadsAndRecovery(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses tab and newline filenames")
	}
	repo := t.TempDir()
	contents := map[string]string{"a file\tname.txt": "first\x00\nsecond", "empty": "", "dir/child": "nested", "line\nbreak": "newline"}
	for name, data := range contents {
		file := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"}} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	src, err := newGitSource(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, name := range []string{"a file\tname.txt", "missing with\ttabs", "dir", "empty", "line\nbreak", "dir/child", "a file\tname.txt"} {
		got, err := src.ReadFile(name)
		want, exists := contents[name]
		if !exists {
			if err == nil {
				t.Fatalf("expected error for %q", name)
			}
			continue
		}
		if err != nil || string(got) != want {
			t.Fatalf("read %q: %q, %v", name, got, err)
		}
	}
	if src.cmd == nil {
		t.Fatal("batch reader not started")
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if src.cmd != nil || src.stdin != nil || src.stdout != nil {
		t.Fatal("batch reader not closed")
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkSourceReadsTheWorkingTree(t *testing.T) {
	repo := t.TempDir()
	files := map[string]string{"a.txt": "hello", "sub/b.txt": "nested"}
	for name, data := range files {
		file := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	w := &workSource{root: repo}
	for name, want := range files {
		got, err := w.ReadFile(name)
		if err != nil || string(got) != want {
			t.Errorf("read %q: %q, %v; want %q", name, got, err, want)
		}
	}
	if _, err := w.ReadFile("missing.txt"); err == nil {
		t.Error("a missing file must fail")
	}
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{"", "a.txt,sub"},
		{"sub", "b.txt"},
	} {
		names, err := w.ListDir(tc.dir)
		if err != nil {
			t.Fatalf("list %q: %v", tc.dir, err)
		}
		if got := strings.Join(names, ","); got != tc.want {
			t.Errorf("list %q: %q; want %q", tc.dir, got, tc.want)
		}
	}
	if _, err := w.ListDir("missing"); err == nil {
		t.Error("a missing directory must fail")
	}
	list, err := w.ListFiles()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(list, ","); got != "a.txt,sub/b.txt" {
		t.Errorf("files: %q; want %q", got, "a.txt,sub/b.txt")
	}
	info := w.Info()
	if info.Label != "working tree" || info.Commit != "" {
		t.Errorf("info: %+v", info)
	}
}

func TestGitSourceListDir(t *testing.T) {
	repo := t.TempDir()
	for name, data := range map[string]string{"a.txt": "hello", "sub/b.txt": "nested"} {
		file := filepath.Join(repo, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-qm", "fixture"}} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	src, err := newGitSource(repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	for _, tc := range []struct {
		dir  string
		want string
	}{
		{"", "a.txt,sub"},
		{"sub", "b.txt"},
	} {
		names, err := src.ListDir(tc.dir)
		if err != nil {
			t.Fatalf("list %q: %v", tc.dir, err)
		}
		if got := strings.Join(names, ","); got != tc.want {
			t.Errorf("list %q: %q; want %q", tc.dir, got, tc.want)
		}
	}
	if _, err := src.ListDir("missing"); err == nil {
		t.Error("a missing directory must fail")
	}
	info := src.Info()
	if info.Label != "HEAD" || info.Commit == "" || info.Subject != "fixture" {
		t.Errorf("info: %+v", info)
	}
}

func TestCleanRelRejectsPathsOutsideTheRepository(t *testing.T) {
	for _, p := range []string{"..", "../x", "a/../../x", "/etc/passwd", "/x"} {
		if got, err := cleanRel(p); err == nil {
			t.Errorf("cleanRel(%q) = %q; want an error", p, got)
		}
	}
	for _, tc := range []struct{ in, want string }{
		{"a/b.kicad_sch", "a/b.kicad_sch"},
		{"./a/../b.kicad_sch", "b.kicad_sch"},
		{"a//b", "a/b"},
		{"", "."},
	} {
		got, err := cleanRel(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("cleanRel(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}
