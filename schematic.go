package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Project is one revision of a (possibly hierarchical) schematic.
type Project struct {
	Name   string            // project name, e.g. "hat_rev8"
	Root   string            // repo-relative path of the root .kicad_sch
	Files  map[string][]byte // every file needed to render, repo-relative
	Parsed map[string]*Node  // parsed schematic files
	Sheets []*SheetInst      // sheet instances in page order
}

// SheetInst is one page of the hierarchy. A sheet file used twice yields two instances.
type SheetInst struct {
	UUIDPath string   // "/<root uuid>/<sheet uuid>/..." as used in symbol instances
	Names    []string // sheet names from the root, empty for the root sheet
	File     string   // repo-relative schematic file
	Page     string   // page number as shown in KiCad
}

func (s *SheetInst) Key() string { return s.UUIDPath }

func (s *SheetInst) DisplayName() string {
	if len(s.Names) == 0 {
		return "/"
	}
	return "/" + strings.Join(s.Names, "/")
}

// LoadProject reads the root schematic and all sub-sheets from src.
func LoadProject(src Source, rootRel string) (*Project, error) {
	p := &Project{
		Name:   strings.TrimSuffix(path.Base(rootRel), ".kicad_sch"),
		Root:   rootRel,
		Files:  map[string][]byte{},
		Parsed: map[string]*Node{},
	}
	if _, err := src.ReadFile(rootRel); err != nil {
		return nil, fmt.Errorf("%s does not exist in this revision", rootRel)
	}
	root, err := p.parse(src, rootRel)
	if err != nil {
		return nil, err
	}
	if err := p.loadAux(src); err != nil {
		return nil, err
	}
	rootPage := "1"
	if si := root.Child("sheet_instances"); si != nil {
		for _, pth := range si.Children("path") {
			if pth.Arg(0) == "/" {
				if pg := pth.Child("page"); pg != nil {
					rootPage = pg.Arg(0)
				}
			}
		}
	}
	rootUUID := root.Child("uuid").Arg(0)
	p.Sheets = append(p.Sheets, &SheetInst{UUIDPath: "/" + rootUUID, File: rootRel, Page: rootPage})
	if err := p.walk(src, root, rootRel, "/"+rootUUID, nil, 0); err != nil {
		return nil, err
	}
	sort.SliceStable(p.Sheets, func(i, j int) bool {
		return pageLess(p.Sheets[i].Page, p.Sheets[j].Page)
	})
	return p, nil
}

// loadAux copies the project file and drawing sheets that rendering may depend on.
func (p *Project) loadAux(src Source) error {
	dir := path.Dir(p.Root)
	names, err := src.ListDir(dir)
	if err != nil {
		return fmt.Errorf("listing %s: %w", dir, err)
	}
	for _, n := range names {
		if strings.HasSuffix(n, ".kicad_pro") || strings.HasSuffix(n, ".kicad_wks") || n == "sym-lib-table" {
			rel := path.Join(dir, n)
			if data, err := src.ReadFile(rel); err == nil {
				p.Files[rel] = data
			}
		}
	}
	// A drawing sheet may also live elsewhere in the repo.
	if data, ok := p.Files[path.Join(dir, p.Name+".kicad_pro")]; ok {
		var pro struct {
			Schematic struct {
				PageLayout string `json:"page_layout_descr_file"`
			} `json:"schematic"`
		}
		if json.Unmarshal(data, &pro) == nil && pro.Schematic.PageLayout != "" {
			wks := strings.ReplaceAll(pro.Schematic.PageLayout, "${KIPRJMOD}", ".")
			if !filepath.IsAbs(wks) {
				if rel, err := cleanRel(path.Join(dir, filepath.ToSlash(wks))); err == nil {
					if data, err := src.ReadFile(rel); err == nil {
						p.Files[rel] = data
					}
				}
			}
		}
	}
	return nil
}

func (p *Project) parse(src Source, rel string) (*Node, error) {
	if n, ok := p.Parsed[rel]; ok {
		return n, nil
	}
	data, err := src.ReadFile(rel)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", rel, err)
	}
	n, err := ParseSexpr(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", rel, err)
	}
	if n.Head() != "kicad_sch" {
		return nil, fmt.Errorf("%s is not a KiCad schematic", rel)
	}
	p.Files[rel] = data
	p.Parsed[rel] = n
	return n, nil
}

func (p *Project) walk(src Source, sch *Node, file, uuidPath string, names []string, depth int) error {
	if depth > 32 {
		return fmt.Errorf("sheet hierarchy too deep (recursive sheets?) at %s", file)
	}
	for _, sh := range sch.Children("sheet") {
		name := propValue(sh, "Sheetname")
		if name == "" {
			name = propValue(sh, "Sheet name")
		}
		sfile := propValue(sh, "Sheetfile")
		if sfile == "" {
			sfile = propValue(sh, "Sheet file")
		}
		uuid := sh.Child("uuid").Arg(0)
		page := ""
		if inst := sh.Child("instances"); inst != nil {
			for _, proj := range inst.Children("project") {
				for _, pth := range proj.Children("path") {
					if pth.Arg(0) == uuidPath {
						page = pth.Child("page").Arg(0)
					}
				}
			}
		}
		rel, err := cleanRel(path.Join(path.Dir(file), filepath.ToSlash(sfile)))
		if err != nil {
			return err
		}
		child, err := p.parse(src, rel)
		if err != nil {
			return err
		}
		childNames := append(append([]string{}, names...), name)
		inst := &SheetInst{UUIDPath: uuidPath + "/" + uuid, Names: childNames, File: rel, Page: page}
		p.Sheets = append(p.Sheets, inst)
		if err := p.walk(src, child, rel, inst.UUIDPath, childNames, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// Materialize writes the project files into dir, preserving repo-relative layout.
func (p *Project) Materialize(dir string) (string, error) {
	for rel, data := range p.Files {
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return "", err
		}
	}
	return filepath.Join(dir, filepath.FromSlash(p.Root)), nil
}

func propValue(n *Node, name string) string {
	for _, pr := range n.Children("property") {
		if pr.Arg(0) == name {
			return pr.Arg(1)
		}
	}
	return ""
}

// pageLess orders page numbers numerically where possible.
func pageLess(a, b string) bool {
	ai, errA := strconv.Atoi(a)
	bi, errB := strconv.Atoi(b)
	if errA == nil && errB == nil {
		return ai < bi
	}
	if (errA == nil) != (errB == nil) {
		return errA == nil
	}
	return a < b
}
