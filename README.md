# dreiecKi

Visual diff of KiCad schematics between two git revisions. It produces one self-contained,
interactive HTML file.

```sh
dreiecki HEAD~1                 # last commit vs. working tree
dreiecki main my-branch         # two branches / tags / commits
dreiecki v7..v8 -o review.html --open
dreiecki -s boards/main/main.kicad_sch HEAD~3   # pick the root schematic explicitly
```

Requirements: `git`, and `kicad-cli` from KiCad 8 or newer for rendering. The viewer works offline in any
current browser.

## What you get

* **Sheets list:** every page of the hierarchy, including sheets that are used more than once,
  each flagged as same / changed / new / deleted.
* **Views:** A, B, Diff (removed ink red, added ink green, rest faded), Swipe,
  synced Side-by-side and Blink.
* **Area navigation:** areas that differ at the pixel level are outlined. Step through them with `n` / `p`.
* **Change list:** components (value, footprint, any field, symbol, unit, DNP/BOM flags, moves),
  labels, wires, junctions, text, sheet symbols and graphics. Click one to zoom to it.

Press `?` in the viewer for keyboard shortcuts. A URL hash such as `#mode=swipe&sheet=/power&area=2`
opens a specific view.

## How it works

For each revision, the needed files are read with `git show` (the root sheet, every sub-sheet it
references, the `.kicad_pro` and the drawing sheet). They are written to a temp directory and
rendered with `kicad-cli sch export svg`. The schematic S-expressions are compared object by object.
Objects are matched by UUID, falling back to reference or content. The pixel comparison runs in the
browser.

## Flags

```
-s FILE              root .kicad_sch (default: auto-detect)
-o FILE              output HTML (default: dreiecki-<a>-<b>.html)
-open                open in browser
-no-drawing-sheet    leave out the title block / frame
-theme NAME          KiCad colour theme for rendering
-kicad-cli PATH      kicad-cli location
-keep-temp           keep the rendered SVGs
```

Build: `go build` (Go 1.27.1+). Cross-compile: `GOOS=windows go build`.

## Releases

Every push to `main` builds Linux, macOS and Windows binaries (amd64 and arm64) and publishes a
GitHub release, bumping the patch version (`v0.1.0`, `v0.1.1`, …). To bump the minor or major
version, tag the commit yourself (`git tag v0.2.0`) and push the tag before or together with the
commit. That commit is released as `v0.2.0`, and later pushes continue from there.
