package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// Source provides read access to a project tree at a given revision.
// Paths are slash-separated and relative to the repository root.
type Source interface {
	ReadFile(rel string) ([]byte, error)
	ListDir(rel string) ([]string, error)
	Info() RevInfo
}

// RevInfo describes a revision for display in the report.
type RevInfo struct {
	Label   string `json:"label"`
	Commit  string `json:"commit"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

func git(repo string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

type gitSource struct {
	repo   string
	commit string
	info   RevInfo
}

func newGitSource(repo, ref string) (*gitSource, error) {
	out, err := git(repo, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("unknown git revision %q", ref)
	}
	commit := strings.TrimSpace(string(out))
	info := RevInfo{Label: ref, Commit: commit}
	if out, err := git(repo, "log", "-1", "--format=%s%x00%an%x00%ad", "--date=short", commit); err == nil {
		parts := strings.SplitN(strings.TrimRight(string(out), "\n"), "\x00", 3)
		if len(parts) == 3 {
			info.Subject, info.Author, info.Date = parts[0], parts[1], parts[2]
		}
	}
	return &gitSource{repo: repo, commit: commit, info: info}, nil
}

func (g *gitSource) ReadFile(rel string) ([]byte, error) {
	return git(g.repo, "show", g.commit+":"+rel)
}

func (g *gitSource) ListDir(rel string) ([]string, error) {
	spec := g.commit + ":" + rel
	if rel == "" || rel == "." {
		spec = g.commit + ":"
	}
	out, err := git(g.repo, "ls-tree", "--name-only", spec)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			names = append(names, l)
		}
	}
	return names, nil
}

func (g *gitSource) Info() RevInfo { return g.info }

// workSource reads the working tree (uncommitted state).
type workSource struct {
	root string
}

func (w *workSource) ReadFile(rel string) ([]byte, error) {
	return os.ReadFile(filepath.Join(w.root, filepath.FromSlash(rel)))
}

func (w *workSource) ListDir(rel string) ([]string, error) {
	ents, err := os.ReadDir(filepath.Join(w.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range ents {
		names = append(names, e.Name())
	}
	return names, nil
}

func (w *workSource) Info() RevInfo {
	return RevInfo{Label: "working tree", Subject: "uncommitted changes"}
}

// cleanRel normalizes a repo-relative path and rejects paths escaping the repo.
func cleanRel(p string) (string, error) {
	c := path.Clean(p)
	if c == ".." || strings.HasPrefix(c, "../") || path.IsAbs(c) {
		return "", fmt.Errorf("path %q is outside the repository", p)
	}
	return c, nil
}
