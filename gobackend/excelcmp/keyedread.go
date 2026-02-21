package excelcmp

import (
	"errors"
	"fmt"
	"strings"

	"github.com/xuri/excelize/v2"
)

type keyedSheet struct {
	Headers   []string
	Key       string
	RowsByKey map[string][]string // normalized key -> full row (len == len(Headers))
}

func loadKeyedSheetXLSX(path string, checkRows int, key string, allowGuess bool) (*keyedSheet, []string, error) {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return &keyedSheet{Headers: nil, Key: key, RowsByKey: map[string][]string{}}, nil, nil
	}
	sheet := sheets[0]

	rowsIter, err := f.Rows(sheet)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rowsIter.Close() }()

	// header
	if !rowsIter.Next() {
		return &keyedSheet{Headers: nil, Key: key, RowsByKey: map[string][]string{}}, nil, nil
	}
	rawHeader, err := rowsIter.Columns()
	if err != nil {
		return nil, nil, err
	}
	headers := normalizeHeaders(rawHeader)

	// peek first N data rows to guess primary key (only for file1)
	if checkRows <= 0 {
		checkRows = 5
	}
	peek := make([][]string, 0, checkRows)
	for len(peek) < checkRows && rowsIter.Next() {
		cols, err := rowsIter.Columns()
		if err != nil {
			return nil, nil, err
		}
		peek = append(peek, padRow(cols, len(headers)))
	}

	keyUsed := strings.TrimSpace(key)
	if allowGuess {
		tbl := &Table{Headers: headers, Rows: peek}
		k, ok := GuessPrimaryKeyColumn(tbl, checkRows)
		if !ok {
			return nil, nil, errors.New("无法猜测主键列，请确保包含明显的编号列")
		}
		keyUsed = k
	}
	if keyUsed == "" {
		return nil, nil, errors.New("主键列为空")
	}
	keyIdx := indexOfHeader(headers, keyUsed)
	if keyIdx < 0 {
		return nil, nil, fmt.Errorf("Excel文件中必须同时包含%q列", keyUsed)
	}

	rowsByKey := make(map[string][]string, 1024)
	dups := make([]string, 0, 10)
	seenDup := make(map[string]struct{})

	add := func(row []string) {
		if keyIdx >= len(row) {
			return
		}
		k := normalizeScalarForCompare(row[keyIdx])
		if strings.TrimSpace(k) == "" {
			return
		}
		if _, ok := rowsByKey[k]; ok {
			if _, already := seenDup[k]; !already {
				seenDup[k] = struct{}{}
				if len(dups) < 10 {
					dups = append(dups, k)
				}
			}
			return
		}
		rowsByKey[k] = row
	}

	for _, r := range peek {
		add(r)
	}
	for rowsIter.Next() {
		cols, err := rowsIter.Columns()
		if err != nil {
			return nil, nil, err
		}
		add(padRow(cols, len(headers)))
	}

	return &keyedSheet{Headers: headers, Key: keyUsed, RowsByKey: rowsByKey}, dups, nil
}

func padRow(cols []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(cols) == n {
		// Rows() already allocates a fresh slice; safe to store directly.
		return cols
	}
	row := make([]string, n)
	copy(row, cols)
	return row
}
