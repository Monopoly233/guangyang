package excelcompare

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

type Table struct {
	Headers []string
	Rows    [][]string // each row aligned with Headers
}

func ReadFirstSheetXLSX(path string) (*Table, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("xlsx 路径为空")
	}
	f, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 xlsx 失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	sheets := f.GetSheetList()
	if len(sheets) == 0 {
		return nil, fmt.Errorf("xlsx 无工作表: %s", filepath.Base(path))
	}
	sheet := sheets[0]

	rows, err := f.Rows(sheet)
	if err != nil {
		return nil, fmt.Errorf("读取工作表失败: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		headers []string
		outRows [][]string
		rowIdx  int
	)
	for rows.Next() {
		rowIdx++
		cols, err := rows.Columns()
		if err != nil {
			return nil, fmt.Errorf("读取行失败 row=%d: %w", rowIdx, err)
		}
		// header
		if rowIdx == 1 {
			headers = make([]string, len(cols))
			for i, c := range cols {
				headers[i] = strings.TrimSpace(c)
			}
			// drop trailing empties
			headers = trimRightEmpty(headers)
			if len(headers) == 0 {
				return nil, errors.New("表头为空")
			}
			continue
		}
		if len(headers) == 0 {
			continue
		}
		r := make([]string, len(headers))
		for i := range headers {
			if i < len(cols) {
				r[i] = cols[i]
			} else {
				r[i] = ""
			}
		}
		outRows = append(outRows, r)
	}

	return &Table{Headers: headers, Rows: outRows}, nil
}

func trimRightEmpty(ss []string) []string {
	i := len(ss) - 1
	for i >= 0 {
		if strings.TrimSpace(ss[i]) != "" {
			break
		}
		i--
	}
	return ss[:i+1]
}

