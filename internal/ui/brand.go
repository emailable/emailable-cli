package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/charmbracelet/lipgloss"
)

// brandArt is the Emailable icon as braille art (16 rows × 36 cols, 2×4 dots/cell).
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

// brandColors maps each brandArt cell to a palette letter; '.' = blank.
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

const brandName = "Emailable"
const brandNameLine = 7  // vertical midpoint of the mark
const blankBraille = '⠀' // U+2800, all-dots-off; keeps spacing constant

// BrandPurple is the primary Emailable brand color.
var BrandPurple = lipgloss.Color("#7e61ff")

// BrandPurpleSoft is a lighter brand tint used for form gutters.
var BrandPurpleSoft = lipgloss.Color("#c7c2ff")

var brandPalette = map[byte]lipgloss.Color{
	'Y': lipgloss.Color("#ffcb60"), // yellow
	'P': lipgloss.Color("#7e61ff"), // purple
	'K': lipgloss.Color("#ff5f7d"), // pink
	'D': lipgloss.Color("#5a3dbe"), // dark purple
	'L': lipgloss.Color("#5fd7aa"), // light teal
	'T': lipgloss.Color("#3fb3a3"), // teal
	'O': lipgloss.Color("#ff9c5b"), // orange
}

// brandGrid is the parsed art. Color grid is canonical; glyph rows are padded
// with blankBraille so trailing-blank loss in the literal can't desync them.
type brandGrid struct {
	glyphs [][]rune
	colors [][]byte
	rows   int
}

var (
	brandOnce   sync.Once
	parsedBrand brandGrid
)

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

var paletteStyles = func() map[byte]lipgloss.Style {
	m := make(map[byte]lipgloss.Style, len(brandPalette))
	for k, c := range brandPalette {
		m[k] = lipgloss.NewStyle().Foreground(c)
	}
	return m
}()

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
