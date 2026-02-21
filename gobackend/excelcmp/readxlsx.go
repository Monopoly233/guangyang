package excelcmp

import (
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

type Table struct {
	Headers []string
	Rows    [][]string
}

func readXLSXFirstSheetTable(path string) (*Table, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return &Table{Headers: nil, Rows: nil}, nil
	}
	sheet := sheets[0]

	rowsIter, err := f.Rows(sheet)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowsIter.Close() }()

	var (
		headers []string
		rows    [][]string
		rowIdx  int
	)
	for rowsIter.Next() {
		rowIdx++
		cols, err := rowsIter.Columns()
		if err != nil {
			return nil, err
		}
		if rowIdx == 1 {
			headers = normalizeHeaders(cols)
			continue
		}
		if len(headers) == 0 {
			// No header row? treat as empty.
			continue
		}
		row := make([]string, len(headers))
		for i := 0; i < len(headers); i++ {
			if i < len(cols) {
				row[i] = cols[i]
			} else {
				row[i] = ""
			}
		}
		// Keep blank rows (pandas sometimes drops, but our semantic compare should ignore via key filtering).
		rows = append(rows, row)
	}

	return &Table{Headers: headers, Rows: rows}, nil
}

func normalizeHeaders(raw []string) []string {
	// pandas behavior: empty header cells become "Unnamed: {i}"
	h := make([]string, len(raw))
	for i, v := range raw {
		s := strings.TrimSpace(v)
		if s == "" {
			s = fmt.Sprintf("Unnamed: %d", i)
		}
		h[i] = s
	}
	// dedupe like pandas: append .1 .2 ...
	seen := make(map[string]int, len(h))
	out := make([]string, len(h))
	for i, name := range h {
		if c, ok := seen[name]; ok {
			c++
			seen[name] = c
			out[i] = fmt.Sprintf("%s.%d", name, c)
		} else {
			seen[name] = 0
			out[i] = name
		}
	}
	return out
}
