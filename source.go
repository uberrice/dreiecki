package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// Source provides read access to a project tree at a given revision.
// Paths are slash-separated and relative to the repository root.
type Source interface {
	ReadFile(rel string) ([]byte, error)
	ListDir(rel string) ([]string, error)
	ListFiles() ([]string, error)
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
	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
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

// ReadFile keeps one cat-file process per revision rather than starting Git for
// every sheet. Newline-containing paths use git show's unambiguous argument form.
func (g *gitSource) ReadFile(rel string) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	var data []byte
	var err error
	if strings.ContainsAny(rel, "\n\r") {
		data, err = git(g.repo, "show", g.commit+":"+rel)
	} else {
		if g.cmd == nil {
			cmd := exec.Command("git", "-C", g.repo, "cat-file", "--batch")
			stdin, pipeErr := cmd.StdinPipe()
			if pipeErr != nil {
				return nil, pipeErr
			}
			stdout, pipeErr := cmd.StdoutPipe()
			if pipeErr != nil {
				stdin.Close()
				return nil, pipeErr
			}
			if pipeErr = cmd.Start(); pipeErr != nil {
				stdin.Close()
				return nil, pipeErr
			}
			g.cmd, g.stdin, g.stdout = cmd, stdin, bufio.NewReader(stdout)
		}
		if _, err = io.WriteString(g.stdin, g.commit+":"+rel+"\n"); err != nil {
			return nil, err
		}
		header, headerErr := g.stdout.ReadString('\n')
		if headerErr != nil {
			return nil, headerErr
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("reading %s: %s", rel, strings.TrimSpace(header))
		}
		size, sizeErr := strconv.Atoi(fields[2])
		if sizeErr != nil || size < 0 {
			return nil, fmt.Errorf("invalid git blob size: %q", fields[2])
		}
		if fields[1] != "blob" {
			if _, err = io.CopyN(io.Discard, g.stdout, int64(size)+1); err != nil {
				return nil, err
			}
			return nil, fmt.Errorf("reading %s: expected blob, got %s", rel, fields[1])
		}
		data = make([]byte, size)
		if _, err = io.ReadFull(g.stdout, data); err != nil {
			return nil, err
		}
		if delimiter, readErr := g.stdout.ReadByte(); readErr != nil || delimiter != '\n' {
			return nil, fmt.Errorf("invalid git blob delimiter: %v", readErr)
		}
	}
	if err != nil {
		return nil, err
	}
	return data, nil
}

func (g *gitSource) Close() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.cmd == nil {
		return nil
	}
	g.stdin.Close()
	err := g.cmd.Wait()
	g.cmd, g.stdin, g.stdout = nil, nil, nil
	return err
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

// ListFiles lists a revision with one recursive tree query. NUL delimiters preserve
// paths containing tabs, newlines, or characters git would otherwise quote.
func (g *gitSource) ListFiles() ([]string, error) {
	out, err := git(g.repo, "ls-tree", "-r", "--name-only", "-z", g.commit)
	if err != nil {
		return nil, err
	}
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 }), nil
}

func (w *workSource) ListFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(w.root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			if e.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(w.root, p)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	return files, err
}
