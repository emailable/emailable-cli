package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// brandArt is a braille-art rendering of the Emailable icon (the segmented
// ring + inner swirl), generated from emailable-icon.svg. Each rune packs a
// 2×4 dot cell; together with brandColors it's 16 rows × 36 cols.
const brandArt = "" +
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣀⣤⣴⣶⣾⣿⣿⣿⣿⣿⣿⣷⣶⣦⣤⣀⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀\n" +
	"⠀⠀⠀⠀⠀⠀⢀⣤⣶⣿⣿⣿⣿⣿⣿⣿⡿⠿⠿⠿⣿⣿⣿⣿⣿⣿⣿⣷⣤⡀⠀⠀⠀⠀⠀⠀\n" +
	"⠀⠀⠀⠀⣠⣶⣿⣿⣿⣿⡿⠛⠋⠉⠀⠀⠀⠀⠀⠀⠀⠀⠈⠉⠛⠿⣿⣿⣿⣿⣷⣄⠀⠀⠀⠀\n" +
	"⠀⠀⢀⣼⣿⣿⣿⡿⠛⠁⠀⠀⠀⠀⢀⣀⣠⣤⣤⣤⣀⣀⠀⠀⠀⠀⠈⠙⢿⣿⣿⣿⣷⡀⠀⠀\n" +
	"⠀⢠⣾⣿⣿⣿⠟⠀⠀⠀⠀⣠⣴⣿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣶⣄⠀⠀⠀⠀⠹⣿⣿⣿⣿⡄⠀\n" +
	"⢀⣿⣿⣿⣿⠃⠀⠀⠀⣠⣾⣿⣿⣿⡿⠟⠛⠛⠛⠛⠻⢿⣿⣿⣿⣷⣄⠀⠀⠀⠘⣿⣿⣿⣿⡄\n" +
	"⣼⣿⣿⣿⡏⠀⠀⠀⢰⣿⣿⣿⡿⠋⠀⠀⠀⠀⠀⠀⠀⠀⠙⢿⣿⣿⣿⣆⠀⠀⠀⠸⣿⣿⣿⣷\n" +
	"⣿⣿⣿⣿⠁⠀⠀⠀⣿⣿⣿⣿⠃⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⣿⣿⣿⣿⠀⠀⠀⠀⣿⣿⣿⣿\n" +
	"⣿⣿⣿⣿⠀⠀⠀⠀⣿⣿⣿⣿⡄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⠀⠀⠀⠀⣿⣿⣿⣿\n" +
	"⢻⣿⣿⣿⡇⠀⠀⠀⠸⣿⣿⣿⣷⣄⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣿⣿⣿⣿⣆⡀⠀⢰⣿⣿⣿⡿\n" +
	"⠈⣿⣿⣿⣿⡄⠀⠀⠀⠙⢿⣿⣿⣿⣷⣦⣤⣀⣀⣠⣤⣄⠀⠀⠘⢿⣿⣿⣿⣿⣷⣿⣿⣿⣿⠃\n" +
	"⠀⠘⣿⣿⣿⣿⣆⠀⠀⠀⠀⠙⠿⣿⣿⣿⣿⣿⣿⣿⣿⣿⣷⡄⠀⠀⠉⠛⠿⣿⣿⣿⣿⣿⠏⠀\n" +
	"⠀⠀⠈⢿⣿⣿⣿⣷⣄⡀⠀⠀⠀⠀⠉⠙⠛⠛⠛⠛⠋⠉⠀⠀⠀⠀⠀⠀⠀⠀⠉⠛⠿⠃⠀⠀\n" +
	"⠀⠀⠀⠀⠙⢿⣿⣿⣿⣿⣶⣤⣀⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀\n" +
	"⠀⠀⠀⠀⠀⠀⠈⠻⢿⣿⣿⣿⣿⣿⣿⣷⣶⣶⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀\n" +
	"⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠙⠛⠿⠿⣿⣿⣿⣿⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀"

// brandColors maps each cell of brandArt to an Emailable brand color. Letters
// index brandPalette; '.' marks a blank (unlit) cell. Same dimensions as
// brandArt, aligned cell-for-cell.
const brandColors = "" +
	"..........KKKKKKKKPPPPPPPPP.........\n" +
	"......KKKKKKKKKKKKPPPPPPPPPPPP......\n" +
	"....KKKKKKKKKK........PPPPPPPPPP....\n" +
	"..KKKKKKKK....LLLLLLLL....PPPPPPPP..\n" +
	".OOOKKK....LLLLLLLLLLLLLL....PPPDDD.\n" +
	"OOOOOO...LLLLLLLLLLLLLLLLLL...DDDDDD\n" +
	"OOOOO...TTTTLL........LLLLLL...DDDDD\n" +
	"OOOOO...TTTTT..........LLLLL....DDDD\n" +
	"OOOO....TTTTT...........LLLL....DDDD\n" +
	"OOOOO...TTTTTT..........LLLLLL.DDDDD\n" +
	"OOOOOO...TTTTTTTTTTTTT..LLLLLLLTDDDD\n" +
	".OOOOYY....TTTTTTTTTTTTT..LLLLLLLLT.\n" +
	"..YYYYYYYY....TTTTTTTT........LLLL..\n" +
	"....YYYYYYYYYY......................\n" +
	"......YYYYYYYYYYYY..................\n" +
	".........YYYYYYYYY.................."

// brandName is the wordmark shown to the right of the mark.
const brandName = "Emailable"

// brandNameLine is the row (0-indexed) the name is rendered beside — the
// vertical middle of the mark.
const brandNameLine = 7

// blankBraille is U+2800, an all-dots-off braille cell. Used as the glyph for
// unrevealed/unlit cells so spacing stays constant.
const blankBraille = '⠀'

// BrandPurple is Emailable's primary brand purple, used for the wordmark text.
var BrandPurple = lipgloss.Color("#7e61ff")

// brandPalette maps the color letters in brandColors to their brand hex.
// '.' has no entry — it's never looked up because blank cells aren't styled.
var brandPalette = map[byte]lipgloss.Color{
	'Y': lipgloss.Color("#ffcb60"), // yellow
	'P': lipgloss.Color("#7e61ff"), // purple
	'K': lipgloss.Color("#ff5f7d"), // pink
	'D': lipgloss.Color("#5a3dbe"), // dark purple
	'L': lipgloss.Color("#5fd7aa"), // light teal
	'T': lipgloss.Color("#3fb3a3"), // teal
	'O': lipgloss.Color("#ff9c5b"), // orange
}

// brandGrid is the parsed, render-ready form of the embedded art. The color
// grid is canonical for width and lit-ness ('.' == blank); glyph rows are
// padded with blankBraille to match, so a lost trailing blank in the literal
// can't desync the two grids.
type brandGrid struct {
	glyphs [][]rune
	colors [][]byte
	rows   int
}

var (
	brandOnce   sync.Once
	parsedBrand brandGrid
)

// grid parses brandArt/brandColors once and caches the result.
func grid() brandGrid {
	brandOnce.Do(func() {
		colorLines := strings.Split(brandColors, "\n")
		glyphLines := strings.Split(brandArt, "\n")
		n := len(colorLines)
		g := brandGrid{
			glyphs: make([][]rune, n),
			colors: make([][]byte, n),
			rows:   n,
		}
		for r := range colorLines {
			cr := []byte(colorLines[r])
			gr := []rune(glyphLines[r])
			// Pad the glyph row to the color row's width; trailing blanks may
			// have been dropped from the literal.
			for len(gr) < len(cr) {
				gr = append(gr, blankBraille)
			}
			g.colors[r] = cr
			g.glyphs[r] = gr
		}
		parsedBrand = g
	})
	return parsedBrand
}

// paletteStyles is brandPalette pre-wrapped as lipgloss styles, built once.
var paletteStyles = func() map[byte]lipgloss.Style {
	m := make(map[byte]lipgloss.Style, len(brandPalette))
	for k, c := range brandPalette {
		m[k] = lipgloss.NewStyle().Foreground(c)
	}
	return m
}()

// renderBrandStatic prints the mark once with no color and no animation, with
// the name beside it. Used when w isn't a color TTY (piped output, NO_COLOR).
func renderBrandStatic(w io.Writer) {
	g := grid()
	for r := 0; r < g.rows; r++ {
		line := string(g.glyphs[r])
		if r == brandNameLine {
			line += "   " + brandName
		}
		fmt.Fprintln(w, line)
	}
}
