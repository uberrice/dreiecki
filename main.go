// Command dreiecki renders two git revisions of a KiCad schematic or board and
// writes a self-contained, interactive HTML diff viewer.
package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed viewer.html
var viewerHTML string

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const worktreeRef = "WORKTREE"

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: dreiecki [flags] <rev-a> [<rev-b>]
       dreiecki [flags] <rev-a>..<rev-b>
       dreiecki [flags]

Compares the KiCad schematic (or, with -pcb, the board) at two git revisions
and writes an interactive HTML report. If <rev-b> is omitted, the working tree
is used (also available explicitly as %s). With no revision at all, the
working tree is rendered on its own, without a comparison.

Examples:
  dreiecki HEAD~1               # last commit vs. uncommitted changes
  dreiecki main feature-branch  # two branches
  dreiecki v7..v8 -o rev.html
  dreiecki -pcb HEAD~1          # board instead of schematic
  dreiecki -pcb                 # just view the current board

Flags:
`, worktreeRef)
	flag.PrintDefaults()
}

func main() {
	var (
		schFlag   = flag.String("s", "", "root `.kicad_sch` file, or .kicad_pcb with -pcb (default: auto-detect from the .kicad_pro in the current directory or repo)")
		pcbFlag   = flag.Bool("pcb", false, "compare the board (.kicad_pcb) instead of the schematic")
		outFlag   = flag.String("o", "", "output HTML `file` (default: dreiecki-<a>-<b>.html, dreiecki-pcb-<a>-<b>.html with -pcb)")
		openFlag  = flag.Bool("open", false, "open the report in the default browser")
		cliFlag   = flag.String("kicad-cli", "", "path to kicad-cli (default: from PATH)")
		themeFlag = flag.String("theme", "", "KiCad color theme to render with")
		noSheet   = flag.Bool("no-drawing-sheet", false, "omit the drawing sheet / title block from the renders")
		keepTmp   = flag.Bool("keep-temp", false, "keep the temporary render directory")
		verFlag   = flag.Bool("version", false, "print the version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *verFlag {
		fmt.Println("dreiecki", version)
		return
	}

	refA, refB, err := parseRefs(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		usage()
		os.Exit(2)
	}
	opt := RenderOptions{KicadCLI: *cliFlag, Theme: *themeFlag, ExcludeDrawSheet: *noSheet}
	pcb := *pcbFlag || strings.HasSuffix(*schFlag, ".kicad_pcb")
	if err := run(refA, refB, *schFlag, *outFlag, pcb, *openFlag, *keepTmp, opt); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// parseRefs returns the two revisions to compare, or two empty strings to view the working tree alone.
func parseRefs(args []string) (string, string, error) {
	switch len(args) {
	case 0:
		return "", "", nil
	case 1:
		if strings.Contains(args[0], "...") {
			return "", "", fmt.Errorf("use A..B, not A...B")
		}
		if a, b, ok := strings.Cut(args[0], ".."); ok {
			if a == "" {
				a = "HEAD"
			}
			if b == "" {
				b = "HEAD"
			}
			return a, b, nil
		}
		return args[0], worktreeRef, nil
	case 2:
		return args[0], args[1], nil
	}
	return "", "", fmt.Errorf("expected at most two revisions")
}

func run(refA, refB, rootPath, outPath string, pcb, open, keepTmp bool, opt RenderOptions) error {
	if opt.KicadCLI == "" {
		p, err := findKicadCLI()
		if err != nil {
			return err
		}
		opt.KicadCLI = p
	}
	view := refA == "" // the working tree on its own, no comparison
	cwd, _ := os.Getwd()
	repo := cwd
	if top, err := git(cwd, "rev-parse", "--show-toplevel"); err == nil {
		repo = strings.TrimSpace(string(top))
	} else if !view {
		return fmt.Errorf("not inside a git repository")
	}

	var srcA, srcB Source
	if view {
		srcA = &workSource{root: repo}
		srcB = srcA
	} else {
		var err error
		if srcA, err = makeSource(repo, refA); err != nil {
			return err
		}
		if srcB, err = makeSource(repo, refB); err != nil {
			return err
		}
	}
	ext := ".kicad_sch"
	if pcb {
		ext = ".kicad_pcb"
	}
	rootRel, err := findRootFile(repo, cwd, rootPath, ext, srcB, srcA)
	if err != nil {
		return err
	}
	if view {
		fmt.Fprintf(os.Stderr, "Rendering %s (%s)\n", rootRel, srcB.Info().Label)
	} else {
		fmt.Fprintf(os.Stderr, "Comparing %s: %s → %s\n", rootRel, srcA.Info().Label, srcB.Info().Label)
	}

	tmp, err := os.MkdirTemp("", "dreiecki-")
	if err != nil {
		return err
	}
	if keepTmp {
		fmt.Fprintln(os.Stderr, "Temporary files kept in", tmp)
	} else {
		defer os.RemoveAll(tmp)
	}

	var rep *Report
	if pcb {
		rep, err = comparePCB(srcA, srcB, rootRel, tmp, opt)
	} else {
		rep, err = compareSchematic(srcA, srcB, rootRel, tmp, opt)
	}
	if err != nil {
		return err
	}
	if view {
		rep.stripA()
	}
	rep.A, rep.B = srcA.Info(), srcB.Info()
	if view {
		rep.B.Subject = "" // nothing is compared, so "uncommitted changes" would mislead
	}
	rep.Root = rootRel
	rep.Generated = time.Now().Format("2006-01-02 15:04")

	if outPath == "" {
		prefix := "dreiecki"
		if pcb {
			prefix += "-pcb"
		}
		outPath = fmt.Sprintf("%s-%s-%s.html", prefix, slug(rep.A), slug(rep.B))
		if view {
			outPath = prefix + "-view.html"
		}
	}
	if err := writeReport(rep, outPath); err != nil {
		return err
	}
	unit := map[bool]string{false: "sheet", true: "page"}[pcb]
	if view {
		fmt.Fprintf(os.Stderr, "%d %s(s)\nWrote %s\n", len(rep.Pages), unit, outPath)
		if open {
			openBrowser(outPath)
		}
		return nil
	}
	total := 0
	changedPages := 0
	for _, p := range rep.Pages {
		total += len(p.Changes)
		if p.Status != "same" {
			changedPages++
		}
	}
	if pcb && len(rep.Pages) > 0 {
		total = len(rep.Pages[0].Changes) // the overview lists every change
	}
	fmt.Fprintf(os.Stderr, "%d %s(s), %d with differences, %d object change(s)\nWrote %s\n",
		len(rep.Pages), unit, changedPages, total, outPath)
	if open {
		openBrowser(outPath)
	}
	return nil
}

func compareSchematic(srcA, srcB Source, rootRel, tmp string, opt RenderOptions) (*Report, error) {
	type side struct {
		proj  *Project
		pages map[string]*RenderedPage
		err   error
	}
	sides := make([]side, 2)
	srcs := []Source{srcA, srcB}
	if srcA == srcB { // viewing one revision: load and render it once
		srcs = srcs[:1]
	}
	var wg sync.WaitGroup
	for i, src := range srcs {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			s := &sides[i]
			s.proj, s.err = LoadProject(src, rootRel)
			if s.err != nil {
				s.err = fmt.Errorf("%s: %w", src.Info().Label, s.err)
				return
			}
			s.pages, s.err = Render(s.proj, filepath.Join(tmp, fmt.Sprint("ab"[i:i+1])), opt)
			if s.err != nil {
				s.err = fmt.Errorf("%s: %w", src.Info().Label, s.err)
			}
		}(i, src)
	}
	wg.Wait()
	if len(srcs) == 1 {
		sides[1] = sides[0]
	}
	for _, s := range sides {
		if s.err != nil {
			return nil, s.err
		}
	}
	return buildReport(sides[0].proj, sides[1].proj, sides[0].pages, sides[1].pages), nil
}

func comparePCB(srcA, srcB Source, rootRel, tmp string, opt RenderOptions) (*Report, error) {
	boards := make([]*Board, 2)
	srcs := []Source{srcA, srcB}
	if srcA == srcB { // viewing one revision: load and render it once
		srcs = srcs[:1]
	}
	for i, src := range srcs {
		b, err := LoadBoard(src, rootRel)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", src.Info().Label, err)
		}
		boards[i] = b
	}
	if len(srcs) == 1 {
		boards[1] = boards[0]
	}
	pages := planPages(boards[0], boards[1])
	renders := make([]map[string]*RenderedPage, 2)
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, src := range srcs {
		wg.Add(1)
		go func(i int, src Source) {
			defer wg.Done()
			renders[i], errs[i] = RenderPCB(boards[i], pages, filepath.Join(tmp, fmt.Sprint("ab"[i:i+1])), opt)
			if errs[i] != nil {
				errs[i] = fmt.Errorf("%s: %w", src.Info().Label, errs[i])
			}
		}(i, src)
	}
	wg.Wait()
	if len(srcs) == 1 {
		renders[1] = renders[0]
	}
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return buildPCBReport(boards[0], boards[1], pages, renders[0], renders[1]), nil
}

func makeSource(repo, ref string) (Source, error) {
	if ref == worktreeRef {
		return &workSource{root: repo}, nil
	}
	return newGitSource(repo, ref)
}

func findKicadCLI() (string, error) {
	if p, err := exec.LookPath("kicad-cli"); err == nil {
		return p, nil
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{"/Applications/KiCad/KiCad.app/Contents/MacOS/kicad-cli"}
	case "windows":
		for _, v := range []string{"10.0", "9.0", "8.0"} {
			candidates = append(candidates, `C:\Program Files\KiCad\`+v+`\bin\kicad-cli.exe`)
		}
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("kicad-cli not found; install KiCad 8+ or pass -kicad-cli")
}

// findRootFile resolves the root schematic or board (by ext) as a repo-relative path.
func findRootFile(repo, cwd, explicit, ext string, srcs ...Source) (string, error) {
	toRel := func(p string) (string, error) {
		abs, err := filepath.Abs(p)
		if err != nil {
			return "", err
		}
		// Resolve symlinks on both sides so paths compare reliably.
		if r, err := filepath.EvalSymlinks(filepath.Dir(abs)); err == nil {
			abs = filepath.Join(r, filepath.Base(abs))
		}
		repoReal := repo
		if r, err := filepath.EvalSymlinks(repo); err == nil {
			repoReal = r
		}
		rel, err := filepath.Rel(repoReal, abs)
		if err != nil {
			return "", err
		}
		return cleanRel(filepath.ToSlash(rel))
	}
	if explicit != "" {
		for _, e := range []string{".kicad_pro", ".kicad_sch", ".kicad_pcb"} {
			if strings.HasSuffix(explicit, e) {
				explicit = strings.TrimSuffix(explicit, e) + ext
			}
		}
		return toRel(explicit)
	}
	// A project file in the current directory.
	if pros, _ := filepath.Glob(filepath.Join(cwd, "*.kicad_pro")); len(pros) == 1 {
		return toRel(strings.TrimSuffix(pros[0], ".kicad_pro") + ext)
	}
	// Otherwise exactly one project in the repo at either revision.
	for _, src := range srcs {
		var found []string
		var walk func(dir string, depth int)
		walk = func(dir string, depth int) {
			if depth > 6 {
				return
			}
			names, err := src.ListDir(dir)
			if err != nil {
				return
			}
			for _, n := range names {
				rel := path.Join(dir, n)
				if strings.HasSuffix(n, ".kicad_pro") {
					found = append(found, strings.TrimSuffix(rel, ".kicad_pro")+ext)
				} else if !strings.HasPrefix(n, ".") && !strings.Contains(n, ".") {
					walk(rel, depth+1)
				}
			}
		}
		walk(".", 0)
		if len(found) == 1 {
			return found[0], nil
		}
		if len(found) > 1 {
			return "", fmt.Errorf("several KiCad projects found (%s); pick one with -s or run from its directory",
				strings.Join(found, ", "))
		}
	}
	return "", fmt.Errorf("no .kicad_pro found; pass the %s file with -s", ext)
}

// Report is the data embedded into the HTML viewer.
type Report struct {
	Kind      string       `json:"kind"` // sch or pcb
	View      bool         `json:"view"` // a single revision, no comparison
	A         RevInfo      `json:"a"`
	B         RevInfo      `json:"b"`
	Root      string       `json:"root"`
	Generated string       `json:"generated"`
	Pages     []PageReport `json:"pages"`
}

type PageReport struct {
	ID      string       `json:"id"`
	Name    string       `json:"name"`
	Page    string       `json:"page"`
	File    string       `json:"file"`
	Status  string       `json:"status"` // same, changed, added, removed
	Width   float64      `json:"width"`
	Height  float64      `json:"height"`
	SvgA    string       `json:"svgA,omitempty"`
	SvgB    string       `json:"svgB,omitempty"`
	Stack   []StackLayer `json:"stack,omitempty"` // stacked board pages: one SVG per layer
	Changes []Change     `json:"changes"`
}

// StackLayer is one layer of a stacked page, as packed SVGs of both revisions.
type StackLayer struct {
	Layer  string `json:"layer"`
	Name   string `json:"name"`
	Copper bool   `json:"copper"`
	SvgA   string `json:"svgA,omitempty"`
	SvgB   string `json:"svgB,omitempty"`
}

// stripA turns a report of a revision against itself into a plain view of it.
func (rep *Report) stripA() {
	rep.View = true
	for i := range rep.Pages {
		p := &rep.Pages[i]
		p.SvgA, p.Status, p.Changes = "", "same", []Change{}
		for j := range p.Stack {
			p.Stack[j].SvgA = ""
		}
	}
}

func buildReport(pa, pb *Project, ra, rb map[string]*RenderedPage) *Report {
	rep := &Report{Kind: "sch"}
	// Pair sheet instances by uuid path, then by name path.
	matchA := map[*SheetInst]*SheetInst{}
	usedA := map[*SheetInst]bool{}
	for _, sb := range pb.Sheets {
		for _, sa := range pa.Sheets {
			if !usedA[sa] && sa.UUIDPath == sb.UUIDPath {
				matchA[sb], usedA[sa] = sa, true
				break
			}
		}
	}
	for _, sb := range pb.Sheets {
		if matchA[sb] != nil {
			continue
		}
		for _, sa := range pa.Sheets {
			if !usedA[sa] && sa.DisplayName() == sb.DisplayName() {
				matchA[sb], usedA[sa] = sa, true
				break
			}
		}
	}
	type pair struct{ a, b *SheetInst }
	var pairs []pair
	for _, sb := range pb.Sheets {
		pairs = append(pairs, pair{matchA[sb], sb})
	}
	for _, sa := range pa.Sheets {
		if !usedA[sa] {
			pairs = append(pairs, pair{sa, nil})
		}
	}

	for i, pr := range pairs {
		ref := pr.b
		if ref == nil {
			ref = pr.a
		}
		page := PageReport{ID: fmt.Sprintf("p%d", i), Name: ref.DisplayName(), Page: ref.Page, File: ref.File}
		var ca, cb *SheetContent
		var svgA, svgB *RenderedPage
		if pr.a != nil {
			ca, svgA = Extract(pa, pr.a), ra[pr.a.Key()]
		} else {
			ca = &SheetContent{}
		}
		if pr.b != nil {
			cb, svgB = Extract(pb, pr.b), rb[pr.b.Key()]
		} else {
			cb = &SheetContent{}
		}
		page.Changes = append(diffComponents(ca.Components, cb.Components), diffItems(ca.Items, cb.Items)...)
		sortChanges(page.Changes)
		if page.Changes == nil {
			page.Changes = []Change{}
		}
		for _, sp := range []*RenderedPage{svgA, svgB} {
			if sp != nil {
				page.Width, page.Height = sp.Width, sp.Height
			}
		}
		if svgA != nil {
			page.SvgA = packSVG(svgA.SVG)
		}
		if svgB != nil {
			page.SvgB = packSVG(svgB.SVG)
		}
		switch {
		case pr.a == nil:
			page.Status = "added"
		case pr.b == nil:
			page.Status = "removed"
		case len(page.Changes) > 0 || svgA == nil || svgB == nil ||
			!bytes.Equal(stripVolatile(svgA.SVG), stripVolatile(svgB.SVG)):
			page.Status = "changed"
		default:
			page.Status = "same"
		}
		rep.Pages = append(rep.Pages, page)
	}
	return rep
}

var titleRe = regexp.MustCompile(`<title>[^<]*</title>`)

// stripVolatile removes the export timestamp so identical sheets compare equal.
func stripVolatile(svg []byte) []byte { return titleRe.ReplaceAll(svg, nil) }

func packSVG(svg []byte) string {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	zw.Write(stripVolatile(svg))
	zw.Close()
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func writeReport(rep *Report, out string) error {
	data, err := json.Marshal(rep)
	if err != nil {
		return err
	}
	kind := map[bool]string{false: "Schematic", true: "PCB"}[rep.Kind == "pcb"]
	title := fmt.Sprintf("%s diff: %s → %s", kind, rep.A.Label, rep.B.Label)
	if rep.View {
		title = fmt.Sprintf("%s: %s", kind, rep.B.Label)
	}
	html := strings.Replace(viewerHTML, "/*__DATA__*/null", string(data), 1)
	html = strings.Replace(html, "<title>Schematic diff</title>", "<title>"+escapeHTML(title)+"</title>", 1)
	return os.WriteFile(out, []byte(html), 0o644)
}

func escapeHTML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

var slugRe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func slug(r RevInfo) string {
	if r.Commit == "" {
		return "worktree"
	}
	s := strings.Trim(slugRe.ReplaceAllString(r.Label, "_"), "_")
	if s == "" || len(s) > 40 {
		s = r.Commit[:8]
	}
	return s
}

func openBrowser(p string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", p)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", p)
	default:
		cmd = exec.Command("xdg-open", p)
	}
	_ = cmd.Start()
}
