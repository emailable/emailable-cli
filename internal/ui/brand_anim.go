package ui

import (
	"fmt"
	"io"
	"math"
	"sort"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// Animation cadence. Cells are revealed over several frames, then the name
// types in letter by letter; the whole sequence runs in well under 1.5s.
const (
	brandSweepInterval = 22 * time.Millisecond
	brandTextInterval  = 45 * time.Millisecond
	brandRevealFrames  = 28 // target frames for the reveal phase
)

// cell is a position in the brand grid.
type cell struct{ row, col int }

// AnimateBrand paints the Emailable mark onto w by tracing its swirl like a
// pen, then reveals the "Emailable" wordmark beside it. It blocks until the
// animation finishes, leaving the fully-rendered mark on screen. When w isn't a
// color TTY (piped output or NO_COLOR) it renders once, statically and
// uncolored.
//
// Rendering is incremental to avoid flicker: it lays down an empty block, then
// each frame paints only the cells revealed that frame, in place. Already-lit
// cells are never repainted — repainting stable cells (and the cursor snapping
// to column 0) every frame is what flickers. The cursor is hidden for the same
// reason. Nothing else must write to w until it returns.
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

	// Lay down an empty, cleared block for paintCells to cursor-up over (so no
	// stale terminal text shows through the mark's blank cells). The \r before
	// each clear guarantees a full-line wipe even if the cursor wasn't at col 0.
	for r := 0; r < g.rows; r++ {
		fmt.Fprint(w, "\r\033[K\n")
	}

	// Reveal one batch of cells per frame, painting only that batch.
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

	// Reveal the wordmark one letter at a time, beside the middle row.
	nameCol := len(g.colors[brandNameLine]) + 3
	for i := 0; i < len(brandName); i++ {
		paintCells(w, g.rows, map[int][]glyphAt{
			brandNameLine: {{col: nameCol + i, s: textStyle.Render(string(brandName[i]))}},
		})
		time.Sleep(brandTextInterval)
	}
}

// glyphAt is a single styled glyph to paint at a column within a row.
type glyphAt struct {
	col int
	s   string
}

// paintCells moves to the top of the rows-tall block, paints each given glyph
// at its (row, col) in place — touching nothing else — and returns the cursor
// just below the block. items[r] must be sorted by col. Painting only changed
// cells (rather than repainting whole lines) is what keeps stable cells from
// flickering. Each \n is followed by \r so column 0 is reached regardless of
// the terminal's newline translation.
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

// The stroke is traced as two hand-authored polylines of (col, row) waypoints
// in grid space (cols 0–35, rows 0–15): the outer ring, then the inner swirl.
// Splitting them lets us match each cell to its own band's path (see
// traceOrder), so a swirl cell can't be grabbed by a nearby ring segment.
var (
	// brandRingPath traces the outer ring clockwise: bottom-center, up the
	// left, over the top, down the right, to the ring's open lower-right.
	brandRingPath = []point{
		{16, 15}, // start: bottom-center
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
		{32, 10}, // ring's open lower-right end
	}
	// brandSwirlPath traces the inner swirl: in at the lower-right tail, then
	// counterclockwise around and in. It ends at the bottom-right inner, not the
	// hollow center — a center endpoint pulls the center-top cells past it so
	// they light last. Early waypoints stay in the teal band (col ≥ 26) so they
	// don't grab inner cells.
	brandSwirlPath = []point{
		{32, 11}, // start: tail tip, entering from the ring
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
		{20, 11}, // bottom-right inner end
	}
)

// point is a waypoint on a brand stroke polyline, in grid coordinates.
type point struct{ col, row float64 }

// pathSamplesPerSeg is how finely each polyline segment is sampled when
// matching cells to their nearest point on the stroke.
const pathSamplesPerSeg = 24

// brandRowAspect weights the row axis when measuring distances: braille cells
// are about twice as tall as wide on screen, so without it the nearest-point
// match would be skewed vertically.
const brandRowAspect = 2.0

// traceOrder reveals cells by tracing the stroke like a pen. Ring cells (the
// pink/purple/orange/yellow band) are matched to brandRingPath and swirl cells
// (teal) to brandSwirlPath; within each, a cell takes the arc-length position
// of its nearest point on that path. The ring is revealed first, then the
// swirl — so each frame lights only the few cells the pen tip is passing, and
// the two bands never bleed into each other's phase.
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

// isSwirlColor reports whether a color letter belongs to the inner swirl (the
// teal band) rather than the outer ring.
func isSwirlColor(c byte) bool { return c == 'T' || c == 'L' }

// samplePath returns evenly-spaced points along the polyline, with the row axis
// scaled by brandRowAspect so distances match what the eye sees.
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

// nearestSample returns the index of the path sample closest to (x, y).
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
