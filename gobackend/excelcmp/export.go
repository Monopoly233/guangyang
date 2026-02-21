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

	// Stream-read xlsx: only peek first 5 rows to guess key, then build key->row map.
	s1, dup1, err := loadKeyedSheetXLSX(file1Path, 5, "", true)
	if err != nil {
		return fmt.Errorf("读取文件1失败: %w", err)
	}
	if len(dup1) > 0 {
		return fmt.Errorf("文件1主键列“%s”存在重复值（示例: %v），请先去重或修正后再比对", s1.Key, dup1)
	}
	s2, dup2, err := loadKeyedSheetXLSX(file2Path, 0, s1.Key, false)
	if err != nil {
		return fmt.Errorf("读取文件2失败: %w", err)
	}
	if len(dup2) > 0 {
		return fmt.Errorf("文件2主键列“%s”存在重复值（示例: %v），请先去重或修正后再比对", s1.Key, dup2)
	}
	art, err := compareArtifactsFromMaps(s1.Headers, s2.Headers, s1.RowsByKey, s2.RowsByKey, s1.Key)
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

	if err := writeSimpleKeyedSheetStream(f, incName, art.IncHeaders, art.IncKeys, art.RightByKey, "无增加项"); err != nil {
		return err
	}
	if err := writeSimpleKeyedSheetStream(f, redName, art.RedHeaders, art.ReducedKeys, art.LeftByKey, "无减少项"); err != nil {
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

func writeSimpleKeyedSheetStream(f *excelize.File, sheet string, headers []string, keys []string, byKey map[string][]string, emptyMsg string) error {
	sw, err := f.NewStreamWriter(sheet)
	if err != nil {
		return err
	}
	rowNum := 1
	if len(headers) == 0 || len(keys) == 0 || byKey == nil {
		if err := sw.SetRow("A1", []interface{}{emptyMsg}); err != nil {
			return err
		}
		return sw.Flush()
	}
	// header
	headerRow := make([]interface{}, len(headers))
	for i, h := range headers {
		headerRow[i] = h
	}
	if err := sw.SetRow(cellAxis(rowNum, 1), headerRow); err != nil {
		return err
	}
	rowNum++

	row := make([]interface{}, len(headers))
	for _, k := range keys {
		r, ok := byKey[k]
		if !ok {
			continue
		}
		for i := 0; i < len(headers); i++ {
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
	if art == nil || len(art.CommonKeys) == 0 {
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

	type normFP struct {
		norm string
		fp   uint64
	}
	type normCache struct {
		m   map[string]normFP
		max int
	}
	cache := &normCache{m: make(map[string]normFP, 2048), max: 80000}
	cachedNormalizeFP := func(raw string) (string, uint64) {
		if len(raw) <= 64 {
			if v, ok := cache.m[raw]; ok {
				return v.norm, v.fp
			}
			n := normalizeScalarForCompare(raw)
			fp := fingerprint64(n)
			if len(cache.m) < cache.max {
				cache.m[raw] = normFP{norm: n, fp: fp}
			}
			return n, fp
		}
		n := normalizeScalarForCompare(raw)
		return n, fingerprint64(n)
	}

	words := diffMaskWords(len(art.OrderedCols))
	mask := make([]uint64, words)
	dirty := make([]int, 0, 64)
	setDiff := func(i int) {
		if i < 0 {
			return
		}
		w := i >> 6
		if w < 0 || w >= len(mask) {
			return
		}
		before := mask[w]
		mask[w] |= 1 << uint(i&63)
		if before == 0 {
			dirty = append(dirty, w)
		}
	}
	resetMask := func() {
		for _, w := range dirty {
			mask[w] = 0
		}
		dirty = dirty[:0]
	}

	firstWritten := false
	writeHeader := func() error {
		// header: [key, col1(file1), col1(file2), ...]
		header := make([]interface{}, 0, 1+len(art.OrderedCols)*2)
		header = append(header, art.Key)
		for _, c := range art.OrderedCols {
			header = append(header, fmt.Sprintf("%s（%s）", c, fn1))
			header = append(header, fmt.Sprintf("%s（%s）", c, fn2))
		}
		return sw.SetRow(cellAxis(rowNum, 1), header)
	}

	for _, k := range art.CommonKeys {
		left := art.LeftByKey[k]
		right := art.RightByKey[k]
		hasDiff := false
		for i := 0; i < len(art.OrderedCols); i++ {
			i1 := -1
			i2 := -1
			if i < len(art.ColIdx1) {
				i1 = art.ColIdx1[i]
			}
			if i < len(art.ColIdx2) {
				i2 = art.ColIdx2[i]
			}
			va := ""
			vb := ""
			if i1 >= 0 && i1 < len(left) {
				va = left[i1]
			}
			if i2 >= 0 && i2 < len(right) {
				vb = right[i2]
			}
			n1, h1 := cachedNormalizeFP(va)
			n2, h2 := cachedNormalizeFP(vb)
			isDiff := false
			if h1 != h2 {
				isDiff = true
			} else if n1 != n2 {
				isDiff = true
			}
			if isDiff {
				hasDiff = true
				setDiff(i)
			}
		}
		if !hasDiff {
			resetMask()
			continue
		}

		if !firstWritten {
			if err := writeHeader(); err != nil {
				return err
			}
			rowNum++
			firstWritten = true
		}

		row := make([]interface{}, 0, 1+len(art.OrderedCols)*2)
		row = append(row, safeCellValue(k))
		// build row cells using computed diff bitset
		for i := 0; i < len(art.OrderedCols); i++ {
			i1 := -1
			i2 := -1
			if i < len(art.ColIdx1) {
				i1 = art.ColIdx1[i]
			}
			if i < len(art.ColIdx2) {
				i2 = art.ColIdx2[i]
			}
			va := ""
			vb := ""
			if i1 >= 0 && i1 < len(left) {
				va = left[i1]
			}
			if i2 >= 0 && i2 < len(right) {
				vb = right[i2]
			}
			isDiff := diffMaskGet(mask, i)

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
		resetMask()
	}
	if !firstWritten {
		if err := sw.SetRow("A1", []interface{}{"无变动项目"}); err != nil {
			return err
		}
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
