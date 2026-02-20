package excelcmp

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/xuri/excelize/v2"
)

// GenerateCompareExportXLSX implements the same 3-sheet export format as the current Python version.
func GenerateCompareExportXLSX(file1Path, file2Path, file1Name, file2Name, outPath string) error {
	if strings.TrimSpace(file1Path) == "" || strings.TrimSpace(file2Path) == "" {
		return errors.New("输入文件路径为空")
	}
	if strings.TrimSpace(outPath) == "" {
		return errors.New("输出路径为空")
	}

	t1, err := readXLSXFirstSheetTable(file1Path)
	if err != nil {
		return fmt.Errorf("读取文件1失败: %w", err)
	}
	t2, err := readXLSXFirstSheetTable(file2Path)
	if err != nil {
		return fmt.Errorf("读取文件2失败: %w", err)
	}

	key, ok := GuessPrimaryKeyColumn(t1, 5)
	if !ok {
		return errors.New("无法猜测主键列，请确保包含明显的编号列")
	}
	if indexOfHeader(t1.Headers, key) < 0 || indexOfHeader(t2.Headers, key) < 0 {
		return fmt.Errorf("Excel文件中必须同时包含%q列", key)
	}

	art, err := CompareArtifacts(t1, t2, key)
	if err != nil {
		return err
	}

	f := excelize.NewFile()
	// Reuse default sheet as the first one to keep sheet order stable and avoid extra sheets.
	defSheet := f.GetSheetName(0)

	base1 := sheetBaseName(file1Name)
	base2 := sheetBaseName(file2Name)
	used := make(map[string]struct{}, 3)

	incName := uniqueSheetName(fmt.Sprintf("%s相比%s增加", base2, base1), used)
	redName := uniqueSheetName(fmt.Sprintf("%s相比%s减少", base2, base1), used)
	diffName := uniqueSheetName("变动项目", used)

	if defSheet == "" {
		defSheet = "Sheet1"
	}
	_ = f.SetSheetName(defSheet, incName)
	f.NewSheet(redName)
	f.NewSheet(diffName)
	f.SetActiveSheet(0)

	// Styles: light red fill + dark red font
	redStyle, _ := f.NewStyle(&excelize.Style{
		Fill: excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"FFC7CE"}},
		Font: &excelize.Font{Color: "9C0006"},
	})

	if err := writeSimpleTableSheetStream(f, incName, art.Increased, "无增加项"); err != nil {
		return err
	}
	if err := writeSimpleTableSheetStream(f, redName, art.Reduced, "无减少项"); err != nil {
		return err
	}
	if err := writeDiffSideBySideStream(f, diffName, art, file1Name, file2Name, redStyle); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	out, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("创建结果文件失败: %w", err)
	}
	defer out.Close()
	if _, err := f.WriteTo(out); err != nil {
		return fmt.Errorf("写入结果文件失败: %w", err)
	}
	return nil
}

func writeSimpleTableSheetStream(f *excelize.File, sheet string, tbl *Table, emptyMsg string) error {
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	rowNum := 1
	if tbl == nil || len(tbl.Headers) == 0 {
		if err := sw.SetRow("A1", []interface{}{emptyMsg}); err != nil {
			return err
		}
		return sw.Flush()
	}
	if len(tbl.Rows) == 0 {
		if err := sw.SetRow("A1", []interface{}{emptyMsg}); err != nil {
			return err
		}
		return sw.Flush()
	}
	// header
	headerRow := make([]interface{}, len(tbl.Headers))
	for i, h := range tbl.Headers {
		headerRow[i] = h
	}
	if err := sw.SetRow(cellAxis(rowNum, 1), headerRow); err != nil {
		return err
	}
	rowNum++
	for _, r := range tbl.Rows {
		row := make([]interface{}, len(tbl.Headers))
		for i := 0; i < len(tbl.Headers); i++ {
			if i < len(r) {
				row[i] = safeCellValue(r[i])
			} else {
				row[i] = ""
			}
		}
		if err := sw.SetRow(cellAxis(rowNum, 1), row); err != nil {
			return err
		}
		rowNum++
	}
	return sw.Flush()
}

func writeDiffSideBySideStream(f *excelize.File, sheet string, art *Artifacts, file1Name, file2Name string, redStyle int) error {
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	rowNum := 1
	if art == nil || len(art.DiffKeys) == 0 {
		if err := sw.SetRow("A1", []interface{}{"无变动项目"}); err != nil {
			return err
		}
		return sw.Flush()
	}

	fn1 := strings.TrimSpace(file1Name)
	fn2 := strings.TrimSpace(file2Name)
	if fn1 == "" {
		fn1 = "文件1"
	}
	if fn2 == "" {
		fn2 = "文件2"
	}

	// header: [key, col1(file1), col1(file2), ...]
	header := make([]interface{}, 0, 1+len(art.OrderedCols)*2)
	header = append(header, art.Key)
	for _, c := range art.OrderedCols {
		header = append(header, fmt.Sprintf("%s（%s）", c, fn1))
		header = append(header, fmt.Sprintf("%s（%s）", c, fn2))
	}
	if err := sw.SetRow(cellAxis(rowNum, 1), header); err != nil {
		return err
	}
	rowNum++

	for _, k := range art.DiffKeys {
		row := make([]interface{}, 0, 1+len(art.OrderedCols)*2)
		row = append(row, safeCellValue(k))
		for _, c := range art.OrderedCols {
			va := ""
			vb := ""
			if art.LeftRows != nil && art.LeftRows[k] != nil {
				va = art.LeftRows[k][c]
			}
			if art.RightRows != nil && art.RightRows[k] != nil {
				vb = art.RightRows[k][c]
			}
			isDiff := false
			if art.DiffMask != nil && art.DiffMask[k] != nil {
				isDiff = art.DiffMask[k][c]
			}

			ca := excelize.Cell{Value: safeCellValue(va)}
			cb := excelize.Cell{Value: safeCellValue(vb)}
			if isDiff && redStyle > 0 {
				ca.StyleID = redStyle
				cb.StyleID = redStyle
			}
			row = append(row, ca, cb)
		}
		if err := sw.SetRow(cellAxis(rowNum, 1), row); err != nil {
			return err
		}
		rowNum++
	}
	return sw.Flush()
}

func safeCellValue(v string) interface{} {
	// Python behavior: pd.isna -> "", list join; in Go we only have string.
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	return v
}

func cellAxis(row, col int) string {
	axis, _ := excelize.CoordinatesToCellName(col, row)
	return axis
}
