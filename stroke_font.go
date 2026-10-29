package main

import (
	"bytes"
	"compress/gzip"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"sync"
)

//go:embed fonts/newstroke/glyphs.gz
var strokeFontData []byte

//go:embed fonts/newstroke/NOTICE.txt
var strokeFontNotices string

//go:embed LICENSE
var projectLicense string

// Keep the encoded glyphs compact and load only when the built-in renderer is used.
var strokeGlyphs = sync.OnceValue(func() []string {
	r, err := gzip.NewReader(bytes.NewReader(strokeFontData))
	if err != nil {
		panic(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		panic(err)
	}
	return strings.Split(string(b), "\n")
})

func strokeGlyph(c rune) string {
	glyphs := strokeGlyphs()
	i := int(c) - ' '
	if i < 0 || i >= len(glyphs) {
		i = '?' - ' '
	}
	return glyphs[i]
}

// Glyph coordinates and advances follow KiCad common/font/stroke_font.cpp.
// The first pair gives the left and right bearings; " R" raises the pen.
const strokeFontScale = 1.0 / 21.0

func strokeAdvance(g string) float64 {
	return float64(int(g[1])-int(g[0])) * strokeFontScale
}

func strokeLineWidth(line string, size float64) float64 {
	var w float64
	for _, c := range line {
		w += strokeAdvance(strokeGlyph(c)) * size
	}
	return w
}

func strokeLinePath(line string, x, baseline float64, st textStyle) string {
	var d strings.Builder
	sx := st.size
	if st.width > 0 {
		sx = st.width
	}
	for _, c := range line {
		g := strokeGlyph(c)
		pen := false
		for i := 2; i+1 < len(g); i += 2 {
			if g[i:i+2] == " R" {
				pen = false
				continue
			}
			gx := float64(int(g[i])-int(g[0])) * strokeFontScale
			gy := float64(int(g[i+1])-'R'-8) * strokeFontScale
			if st.italic {
				gx -= gy / 8
			}
			cmd := 'M'
			if pen {
				cmd = 'L'
			}
			fmt.Fprintf(&d, "%c%s %s", cmd, ff(x+gx*sx), ff(baseline+gy*st.size))
			pen = true
		}
		x += strokeAdvance(g) * sx
	}
	return d.String()
}
