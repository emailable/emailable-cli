package ui

import (
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const (
	brandSweepInterval = 22 * time.Millisecond
	brandTextInterval  = 45 * time.Millisecond
	brandRevealFrames  = 28 // target frames for the reveal phase
)

type cell struct{ row, col int }

// AnimateBrand traces the brand mark onto w cell-by-cell, then types the
// wordmark. Blocks until done; nothing else must write to w until it returns.
func AnimateBrand(w io.Writer) {
	if !IsTTY(w) {
		renderBrandStatic(w)
		return
	}

	g := grid()
	order := traceOrder(g)
	if len(order) == 0 {
		renderBrandStatic(w)
		return
	}

	batch := max(1, len(order)/brandRevealFrames)
	numBatches := (len(order) + batch - 1) / batch
	textStyle := lipgloss.NewStyle().Foreground(BrandPurple).Bold(true)

	fmt.Fprint(w, "\033[?25l") // hide the cursor for the duration
	defer fmt.Fprint(w, "\033[?25h")

	// \r before each clear guarantees a full-line wipe even if the cursor
	// wasn't at col 0.
	for r := 0; r < g.rows; r++ {
		fmt.Fprint(w, "\r\033[K\n")
	}

	for f := 0; f < numBatches; f++ {
		items := map[int][]glyphAt{}
		for _, c := range order[f*batch : min((f+1)*batch, len(order))] {
			items[c.row] = append(items[c.row], glyphAt{
				col: c.col,
				s:   paletteStyles[g.colors[c.row][c.col]].Render(string(g.glyphs[c.row][c.col])),
			})
		}
		for r := range items {
			sort.Slice(items[r], func(i, j int) bool { return items[r][i].col < items[r][j].col })
		}
		paintCells(w, g.rows, items)
		time.Sleep(brandSweepInterval)
	}

	nameCol := len(g.colors[brandNameLine]) + 3
	for i := 0; i < len(brandName); i++ {
		paintCells(w, g.rows, map[int][]glyphAt{
			brandNameLine: {{col: nameCol + i, s: textStyle.Render(string(brandName[i]))}},
		})
		time.Sleep(brandTextInterval)
	}
}

type glyphAt struct {
	col int
	s   string
}

// paintCells paints only the given cells in place (cursor-up, then per-cell
// positioning). Repainting whole lines would flicker stable cells.
// items[r] must be sorted by col.
func paintCells(w io.Writer, rows int, items map[int][]glyphAt) {
	fmt.Fprintf(w, "\033[%dA", rows) // up to the first row of the block
	for r := 0; r < rows; r++ {
		if r > 0 {
			fmt.Fprint(w, "\n")
		}
		fmt.Fprint(w, "\r") // column 0 of this row
		cur := 0
		for _, it := range items[r] {
			if it.col > cur {
				fmt.Fprintf(w, "\033[%dC", it.col-cur) // step right to the cell
			}
			fmt.Fprint(w, it.s)
			cur = it.col + 1
		}
	}
	fmt.Fprint(w, "\n\r") // step below the block, back to column 0
}

// Two hand-authored polylines in grid space (cols 0–35, rows 0–15). Splitting
// ring and swirl lets each cell match only its own band; without the split,
// nearby swirl cells would be grabbed by ring segments.
var (
	brandRingPath = []point{
		{16, 15},
		{10, 14},
		{5, 13},
		{2, 9},
		{1, 6},
		{3, 3},
		{7, 1},
		{14, 0},
		{21, 0},
		{27, 2},
		{32, 5},
		{34, 8},
		{32, 10},
	}
	// Ends at the bottom-right inner rather than the hollow center — a center
	// endpoint pulls center-top cells past it so they light last.
	brandSwirlPath = []point{
		{32, 11},
		{29, 11},
		{26, 10},
		{25, 8},
		{24, 6},
		{21, 4},
		{16, 3},
		{12, 5},
		{10, 8},
		{11, 11},
		{15, 12},
		{20, 11},
	}
)

type point struct{ col, row float64 }

const pathSamplesPerSeg = 24

// Braille cells are ~2× taller than wide; without this the nearest-point
// match would be skewed vertically.
const brandRowAspect = 2.0

func traceOrder(g brandGrid) []cell {
	ring := samplePath(brandRingPath)
	swirl := samplePath(brandSwirlPath)

	type ranked struct {
		cell
		band int // 0 = ring (revealed first), 1 = swirl
		idx  int // nearest sample index along that band's path
	}
	var rs []ranked
	for r := 0; r < g.rows; r++ {
		for col, ch := range g.colors[r] {
			if ch == '.' {
				continue
			}
			band, path := 0, ring
			if isSwirlColor(ch) {
				band, path = 1, swirl
			}
			rs = append(rs, ranked{
				cell: cell{row: r, col: col},
				band: band,
				idx:  nearestSample(path, float64(col), float64(r)*brandRowAspect),
			})
		}
	}
	sort.SliceStable(rs, func(i, j int) bool {
		if rs[i].band != rs[j].band {
			return rs[i].band < rs[j].band
		}
		return rs[i].idx < rs[j].idx
	})

	out := make([]cell, len(rs))
	for i, r := range rs {
		out[i] = r.cell
	}
	return out
}

func isSwirlColor(c byte) bool { return c == 'T' || c == 'L' }

func samplePath(path []point) [][2]float64 {
	var pts [][2]float64
	for seg := 0; seg+1 < len(path); seg++ {
		a, b := path[seg], path[seg+1]
		for s := 0; s < pathSamplesPerSeg; s++ {
			f := float64(s) / float64(pathSamplesPerSeg)
			pts = append(pts, [2]float64{a.col + (b.col-a.col)*f, (a.row + (b.row-a.row)*f) * brandRowAspect})
		}
	}
	last := path[len(path)-1]
	pts = append(pts, [2]float64{last.col, last.row * brandRowAspect})
	return pts
}

func nearestSample(pts [][2]float64, x, y float64) int {
	best, bestD := 0, math.MaxFloat64
	for i, p := range pts {
		dx, dy := x-p[0], y-p[1]
		if d := dx*dx + dy*dy; d < bestD {
			bestD, best = d, i
		}
	}
	return best
}
