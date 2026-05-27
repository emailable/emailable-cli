package ui

import (
	"bytes"
	"strings"
	"testing"
)

// TestBrandGridAligned guards the embedded art: the glyph and color grids must
// stay the same shape so a cell's glyph and color never desync.
func TestBrandGridAligned(t *testing.T) {
	g := grid()
	if g.rows == 0 {
		t.Fatal("empty brand grid")
	}
	for r := 0; r < g.rows; r++ {
		if len(g.glyphs[r]) < len(g.colors[r]) {
			t.Errorf("row %d: glyphs (%d) shorter than colors (%d)", r, len(g.glyphs[r]), len(g.colors[r]))
		}
	}
	// Every non-blank color letter must have a palette entry.
	for r := 0; r < g.rows; r++ {
		for _, col := range g.colors[r] {
			if col == '.' {
				continue
			}
			if _, ok := brandPalette[col]; !ok {
				t.Errorf("row %d: color %q has no palette entry", r, string(col))
			}
		}
	}
}

// TestTraceOrderCoversLitCells ensures the reveal order visits each lit cell
// exactly once — no gaps, no duplicates, no blanks.
func TestTraceOrderCoversLitCells(t *testing.T) {
	g := grid()
	lit := 0
	for r := 0; r < g.rows; r++ {
		for _, col := range g.colors[r] {
			if col != '.' {
				lit++
			}
		}
	}
	order := traceOrder(g)
	if len(order) != lit {
		t.Errorf("returned %d cells, want %d lit", len(order), lit)
	}
	seen := map[cell]bool{}
	for _, c := range order {
		if seen[c] {
			t.Errorf("duplicate cell %+v", c)
		}
		seen[c] = true
		if g.colors[c.row][c.col] == '.' {
			t.Errorf("order includes blank cell %+v", c)
		}
	}
}

// TestRenderBrandStatic checks the non-TTY fallback: one line per row, with the
// wordmark beside the middle row. Also emits the mark via t.Log for eyeballing.
func TestRenderBrandStatic(t *testing.T) {
	var buf bytes.Buffer
	renderBrandStatic(&buf)
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	g := grid()
	if len(lines) != g.rows {
		t.Fatalf("static render has %d lines, want %d", len(lines), g.rows)
	}
	if !strings.Contains(lines[brandNameLine], brandName) {
		t.Errorf("name %q not on line %d: %q", brandName, brandNameLine, lines[brandNameLine])
	}
	t.Logf("\n%s", buf.String())
}
