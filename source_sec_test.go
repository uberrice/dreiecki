package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// secWrite writes a file, creating the parent directories.
func secWrite(t *testing.T, file, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// secRepo creates a git repository with a single commit. The identity is set in
// the repository, so that the commands work without a global git identity.
func secRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	secWrite(t, filepath.Join(repo, "hw.kicad_pro"), "{}")
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.name", "Test"},
		{"config", "user.email", "test@example.invalid"},
		{"add", "."},
		{"commit", "-qm", "fixture"},
	} {
		if _, err := git(repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	return repo
}

func secSkipNoSymlink(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks needs privileges on Windows")
	}
}

// A symlink that stays inside the worktree is allowed and reads its target.
func TestSecWorkSourceAllowsSymlinkInsideRoot(t *testing.T) {
	secSkipNoSymlink(t)
	root := t.TempDir()
	secWrite(t, filepath.Join(root, "inner.kicad_sch"), "inside")
	secWrite(t, filepath.Join(root, "dir", "sheet.kicad_sch"), "sheet")
	for _, link := range []struct{ name, target string }{
		{"hw.kicad_sch", filepath.Join(root, "inner.kicad_sch")},
		{"sub", filepath.Join(root, "dir")},
	} {
		if err := os.Symlink(link.target, filepath.Join(root, link.name)); err != nil {
			t.Fatal(err)
		}
	}
	src := &workSource{root: root}
	data, err := src.ReadFile("hw.kicad_sch")
	if err != nil {
		t.Fatalf("a symlink inside the root must be readable: %v", err)
	}
	if string(data) != "inside" {
		t.Fatalf("got %q, want %q", data, "inside")
	}
	names, err := src.ListDir("sub")
	if err != nil {
		t.Fatalf("a symlinked directory inside the root must be listed: %v", err)
	}
	if len(names) != 1 || names[0] != "sheet.kicad_sch" {
		t.Fatalf("got %v, want [sheet.kicad_sch]", names)
	}
}

// A path that does not exist keeps the usual not-found error.
func TestSecWorkSourceMissingFileIsNotFound(t *testing.T) {
	root := t.TempDir()
	src := &workSource{root: root}
	_, err := src.ReadFile("missing.kicad_sch")
	if err == nil {
		t.Fatal("expected an error for a missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected a not-found error, got %v", err)
	}
	if strings.Contains(err.Error(), "outside") {
		t.Fatalf("a missing file must not be reported as a security error: %v", err)
	}
}

// cleanRel rejects a path with a NUL byte.
func TestSecCleanRelRejectsEscapes(t *testing.T) {
	for _, tc := range []struct {
		path   string
		reject bool
	}{
		{"a\x00b", true},
		{"a/b", false},
	} {
		got, err := cleanRel(tc.path)
		if tc.reject && err == nil {
			t.Errorf("cleanRel(%q) = %q; want an error", tc.path, got)
		}
		if !tc.reject && err != nil {
			t.Errorf("cleanRel(%q): %v; want it accepted", tc.path, err)
		}
	}
}

// An unreadable directory must not abort the walk.
func TestSecWorkSourceListFilesSkipsUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions do not apply")
	}
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	secWrite(t, filepath.Join(locked, "hidden.kicad_pro"), "{}")
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	secWrite(t, filepath.Join(root, "visible.kicad_pro"), "{}")
	src := &workSource{root: root}
	files, err := src.ListFiles()
	if err != nil {
		t.Fatalf("the walk must skip an unreadable directory, got %v", err)
	}
	for _, f := range files {
		if strings.HasPrefix(f, "locked/") {
			t.Fatalf("the walk listed a file of an unreadable directory: %v", files)
		}
	}
	found := false
	for _, f := range files {
		if f == "visible.kicad_pro" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the walk missed a plain file: %v", files)
	}
}

// An unknown revision keeps the error reported by git.
func TestSecGitSourceUnknownRevisionKeepsCause(t *testing.T) {
	repo := secRepo(t)
	_, err := newGitSource(repo, "no-such-revision")
	if err == nil {
		t.Fatal("expected an error for an unknown revision")
	}
	if !strings.Contains(err.Error(), "unknown git revision") {
		t.Fatalf("the friendly message is missing: %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-revision") {
		t.Fatalf("the error must name the revision: %v", err)
	}
	cause := errors.Unwrap(err)
	if cause == nil {
		t.Fatalf("the git error must be preserved: %v", err)
	}
	if !strings.Contains(cause.Error(), "no-such-revision") {
		t.Fatalf("the cause must name the revision: %v", cause)
	}
}

// A revision that already carries a peel operator must resolve, and it must not
// be peeled a second time.
func TestSecGitSourceAcceptsPeeledRef(t *testing.T) {
	repo := secRepo(t)
	if _, err := git(repo, "tag", "v1.0"); err != nil {
		t.Fatal(err)
	}
	want, err := git(repo, "rev-parse", "v1.0^{commit}")
	if err != nil {
		t.Fatal(err)
	}
	src, err := newGitSource(repo, "v1.0^{}")
	if err != nil {
		t.Fatalf("a peeled revision must resolve: %v", err)
	}
	defer src.Close()
	if src.info.Commit != strings.TrimSpace(string(want)) {
		t.Fatalf("got commit %q, want %q", src.info.Commit, strings.TrimSpace(string(want)))
	}
	// A tree is not a commit. The peel operator of the revision must be left
	// alone, or git would report the error instead of our own.
	if _, err := newGitSource(repo, "v1.0^{tree}"); err == nil {
		t.Fatal("a tree must not be accepted as a revision")
	} else if !strings.Contains(err.Error(), "not a commit") {
		t.Fatalf("the peel operator was doubled: %v", err)
	}
}

// A revision that looks like an option must not reach git as one.
func TestSecGitSourceRejectsOptionLikeRef(t *testing.T) {
	repo := secRepo(t)
	for _, ref := range []string{"--version", "--help", "-x", "--end-of-options"} {
		src, err := newGitSource(repo, ref)
		if err == nil {
			src.Close()
			t.Errorf("revision %q must be rejected", ref)
			continue
		}
		if !strings.Contains(err.Error(), ref) {
			t.Errorf("the error for %q must name the revision: %v", ref, err)
		}
		// The revision is passed after --end-of-options, so that git cannot
		// read it as an option.
		if !strings.Contains(err.Error(), "--end-of-options "+ref) {
			t.Errorf("the revision %q was not passed after --end-of-options: %v", ref, err)
		}
	}
}

// The resolved revision must be the commit, not the object it points at.
func TestSecGitSourceResolvesAnnotatedTag(t *testing.T) {
	repo := secRepo(t)
	if _, err := git(repo, "tag", "-a", "v1.0", "-m", "release"); err != nil {
		t.Fatal(err)
	}
	src, err := newGitSource(repo, "v1.0")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	want, err := git(repo, "rev-parse", "v1.0^{commit}")
	if err != nil {
		t.Fatal(err)
	}
	if src.info.Commit != strings.TrimSpace(string(want)) {
		t.Fatalf("got commit %q, want %q", src.info.Commit, strings.TrimSpace(string(want)))
	}
	if _, err := src.ReadFile("hw.kicad_pro"); err != nil {
		t.Fatalf("reading from the resolved revision: %v", err)
	}
}

// The revision must not be usable to smuggle a path into git.
func TestSecGitSourceRevisionIsNotAPath(t *testing.T) {
	repo := secRepo(t)
	secWrite(t, filepath.Join(repo, "hw.kicad_sch"), "worktree")
	if _, err := git(repo, "add", "."); err != nil {
		t.Fatal(err)
	}
	if _, err := git(repo, "commit", "-qm", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := newGitSource(repo, "HEAD:hw.kicad_sch"); err == nil {
		t.Fatal("a path must not be accepted as a revision")
	}
	if _, err := newGitSource(repo, "HEAD^{tree}"); err == nil {
		t.Fatal("a tree must not be accepted as a revision")
	}
}
