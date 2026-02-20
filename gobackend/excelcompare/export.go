package excelcompare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

type ExportOptions struct {
	File1Name string
	File2Name string
}

func ExportResultXLSX(outPath string, res *CompareResult, opt ExportOptions) error {
	if res == nil {
		return errors.New("compare result 为空")
	}
	outPath = strings.TrimSpace(outPath)
	if outPath == "" {
		return errors.New("输出路径为空")
	}

	f := excelize.NewFile()
	defer func() { _ = f.Close() }()

	base1 := sheetBaseName(opt.File1Name)
	base2 := sheetBaseName(opt.File2Name)
	used := map[string]bool{}

	addSheet := uniqueSheetName(fmt.Sprintf("%s相比%s增加", base2, base1), used)
	delSheet := uniqueSheetName(fmt.Sprintf("%s相比%s减少", base2, base1), used)
	diffSheet := uniqueSheetName("变动项目", used)

	// Remove default sheet and create our own.
	def := f.GetSheetName(0)
	if def != "" {
		_ = f.DeleteSheet(def)
	}
	f.NewSheet(addSheet)
	f.NewSheet(delSheet)
	f.NewSheet(diffSheet)
	f.SetActiveSheet(0)

	// Styles
	headerStyle, _ := f.NewStyle(&excelize.Style{Font: &excelize.Font{Bold: true}})
	redStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Color: []string{"#FFC7CE"}, Pattern: 1},
		Font: &excelize.Font{Color: "#9C0006"},
	})

	// Added/Removed (keep their original column orders by writing headers explicitly)
	if err := writeSimpleTable(f, addSheet, res.File2Headers, res.Added, headerStyle, "无增加项"); err != nil {
		return err
	}
	if err := writeSimpleTable(f, delSheet, res.File1Headers, res.Removed, headerStyle, "无减少项"); err != nil {
		return err
	}

	// Changed side-by-side
	if err := writeChangedTable(f, diffSheet, res, opt, headerStyle, redStyle); err != nil {
		return err
	}

	if err := ensureDir(outPath); err != nil {
		return err
	}
	return f.SaveAs(outPath)
}

func writeSimpleTable(f *excelize.File, sheet string, headers []string, rows [][]string, headerStyle int, emptyMsg string) error {
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	defer func() { _ = sw.Flush() }()

	headers = append([]string(nil), headers...)
	for i := range headers {
		headers[i] = strings.TrimSpace(headers[i])
	}
	headers = trimRightEmpty(headers)

	if len(headers) == 0 {
		headers = []string{"(无表头)"}
	}

	// Header row
	h := make([]any, 0, len(headers))
	for _, c := range headers {
		h = append(h, excelize.Cell{Value: c, StyleID: headerStyle})
	}
	if err := sw.SetRow("A1", h); err != nil {
		return err
	}

	if len(rows) == 0 {
		_ = sw.SetRow("A2", []any{excelize.Cell{Value: emptyMsg}})
		return nil
	}

	for i, r := range rows {
		row := make([]any, 0, len(headers))
		for j := range headers {
			if j < len(r) {
				row = append(row, r[j])
			} else {
				row = append(row, "")
			}
		}
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := sw.SetRow(cell, row); err != nil {
			return err
		}
	}
	return nil
}

func writeChangedTable(f *excelize.File, sheet string, res *CompareResult, opt ExportOptions, headerStyle, redStyle int) error {
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	defer func() { _ = sw.Flush() }()

	if len(res.ChangedKeys) == 0 {
		_ = sw.SetRow("A1", []any{excelize.Cell{Value: "无变动项目"}})
		return nil
	}

	f1 := strings.TrimSpace(opt.File1Name)
	if f1 == "" {
		f1 = "文件1"
	}
	f2 := strings.TrimSpace(opt.File2Name)
	if f2 == "" {
		f2 = "文件2"
	}

	// Header
	h := make([]any, 0, 1+len(res.OrderedCols)*2)
	h = append(h, excelize.Cell{Value: res.KeyCol, StyleID: headerStyle})
	for _, c := range res.OrderedCols {
		h = append(h, excelize.Cell{Value: fmt.Sprintf("%s（%s）", c, f1), StyleID: headerStyle})
		h = append(h, excelize.Cell{Value: fmt.Sprintf("%s（%s）", c, f2), StyleID: headerStyle})
	}
	if err := sw.SetRow("A1", h); err != nil {
		return err
	}

	for i, k := range res.ChangedKeys {
		lv := res.Left[k]
		rv := res.Right[k]
		dm := res.DiffMask[k]

		row := make([]any, 0, 1+len(res.OrderedCols)*2)
		row = append(row, k)
		for j := range res.OrderedCols {
			v1 := ""
			if j < len(lv) {
				v1 = lv[j]
			}
			v2 := ""
			if j < len(rv) {
				v2 = rv[j]
			}
			diff := false
			if j < len(dm) {
				diff = dm[j]
			}
			if diff {
				row = append(row, excelize.Cell{Value: v1, StyleID: redStyle})
				row = append(row, excelize.Cell{Value: v2, StyleID: redStyle})
			} else {
				row = append(row, v1)
				row = append(row, v2)
			}
		}
		cell, _ := excelize.CoordinatesToCellName(1, i+2)
		if err := sw.SetRow(cell, row); err != nil {
			return err
		}
	}
	return nil
}

func sheetBaseName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "文件"
	}
	name = filepath.Base(name)
	if i := strings.LastIndex(name, "."); i > 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "文件"
	}
	return name
}

func safeSheetName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "Sheet"
	}
	for _, ch := range []string{":", "\\", "/", "?", "*", "[", "]"} {
		s = strings.ReplaceAll(s, ch, "_")
	}
	if len(s) > 31 {
		s = s[:31]
	}
	return s
}

func uniqueSheetName(name string, used map[string]bool) string {
	base := safeSheetName(name)
	cand := base
	i := 2
	for used[cand] {
		suffix := fmt.Sprintf("_%d", i)
		maxLen := 31 - len(suffix)
		p := base
		if len(p) > maxLen {
			p = p[:maxLen]
		}
		cand = p + suffix
		i++
	}
	used[cand] = true
	return cand
}

func ensureDir(p string) error {
	dir := filepath.Dir(p)
	if dir == "" || dir == "." {
		return nil
	}
	return os.MkdirAll(dir, 0o755)
}

