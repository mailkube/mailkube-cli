package output

import "strings"

// columnGap is the run of spaces between two columns.
const columnGap = "  "

// Table is a set of rows rendered as aligned columns.
//
// Alignment is by display width, not by byte or rune count, so a CJK subject or an emoji in a
// cell cannot push the columns after it out of line. That is why this exists at all rather than
// each command reaching for a printf width, which counts bytes and gets both cases wrong.
//
// Nothing here truncates. A column that is too wide for the terminal wraps in the terminal
// rather than being shortened here, because deciding which value to cut is a decision only the
// command knows how to make: a request id shortened to fit cannot be looked up, and a base URL
// elided from the right loses the trailing slash that changes what it means.
type Table struct {
	// Headers are the column titles. An empty slice renders the rows with no header line.
	Headers []string
	// Rows are the body, each of which should have as many cells as there are headers.
	Rows [][]string
}

// Lines renders the table.
//
// Cells are sanitised, because a table is the one place a value from a server sits beside values
// the CLI wrote, and a cell that could contain a newline or a cursor movement could forge a row.
func (t Table) Lines() []string {
	widths := t.widths()

	var lines []string
	if len(t.Headers) > 0 {
		lines = append(lines, renderRow(t.Headers, widths))
	}
	for _, row := range t.Rows {
		lines = append(lines, renderRow(row, widths))
	}
	return lines
}

// widths is the display width of the widest cell in each column.
func (t Table) widths() []int {
	widths := make([]int, t.columns())
	for _, row := range append([][]string{t.Headers}, t.Rows...) {
		for i, cell := range row {
			if w := DisplayWidth(Sanitize(cell)); i < len(widths) && w > widths[i] {
				widths[i] = w
			}
		}
	}
	return widths
}

// columns is the widest row's cell count, so a short row does not silently lose a column.
func (t Table) columns() int {
	count := len(t.Headers)
	for _, row := range t.Rows {
		if len(row) > count {
			count = len(row)
		}
	}
	return count
}

// renderRow pads each cell to its column width, leaving the last one unpadded so no line carries
// trailing whitespace — which a golden file would record and an editor would then strip.
func renderRow(row []string, widths []int) string {
	var b strings.Builder
	for i, cell := range row {
		cell = Sanitize(cell)
		if i > 0 {
			b.WriteString(columnGap)
		}
		b.WriteString(cell)
		if i < len(row)-1 && i < len(widths) {
			b.WriteString(strings.Repeat(" ", widths[i]-DisplayWidth(cell)))
		}
	}
	// Trailing whitespace is trimmed rather than avoided cell by cell: a row whose last cell
	// is empty would otherwise end in the padding of the cell before it, and a golden file
	// records that padding while most editors strip it on save.
	return strings.TrimRight(b.String(), " ")
}
