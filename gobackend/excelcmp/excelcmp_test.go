package excelcmp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"
)

func writeXLSX(t *testing.T, path string, headers []string, rows [][]string) {
	t.Helper()
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	if sheet == "" {
		sheet = "Sheet1"
	}
	// header
	for i, h := range headers {
		axis, _ := excelize.CoordinatesToCellName(i+1, 1)
		_ = f.SetCellValue(sheet, axis, h)
	}
	for r := 0; r < len(rows); r++ {
		for c := 0; c < len(rows[r]); c++ {
			axis, _ := excelize.CoordinatesToCellName(c+1, r+2)
			_ = f.SetCellValue(sheet, axis, rows[r][c])
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.SaveAs(path); err != nil {
		t.Fatal(err)
	}
}

func TestGuessPrimaryKeyColumn(t *testing.T) {
	tbl := &Table{
		Headers: []string{"资产编号", "名称"},
		Rows: [][]string{
			{"1001", "a"},
			{"1002", "b"},
			{"1003", "c"},
		},
	}
	got, ok := GuessPrimaryKeyColumn(tbl, 5)
	if !ok || got != "资产编号" {
		t.Fatalf("expected 主键=资产编号, got=%q ok=%v", got, ok)
	}
}

func TestCompareArtifactsDuplicateKeyError(t *testing.T) {
	t1 := &Table{
		Headers: []string{"编号", "名称"},
		Rows: [][]string{
			{"1", "a"},
			{"1", "b"},
		},
	}
	t2 := &Table{
		Headers: []string{"编号", "名称"},
		Rows:    [][]string{{"1", "a"}},
	}
	_, err := CompareArtifacts(t1, t2, "编号")
	if err == nil {
		t.Fatalf("expected error")
	}
	if want := "文件1主键列“编号”存在重复值"; !contains(err.Error(), want) {
		t.Fatalf("unexpected err: %v", err)
	}
}

func TestOrderedColsUnion(t *testing.T) {
	t1 := &Table{Headers: []string{"编号", "A", "B"}}
	t2 := &Table{Headers: []string{"编号", "B", "C", "A"}}
	got := orderedUnionCols(t1.Headers, t2.Headers, "编号")
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got=%v want=%v", got, want)
		}
	}
}

func TestExportThreeSheetsAndRedStyle(t *testing.T) {
	dir := t.TempDir()
	f1 := filepath.Join(dir, "old.xlsx")
	f2 := filepath.Join(dir, "new.xlsx")
	out := filepath.Join(dir, "out.xlsx")

	// file1: key=编号
	writeXLSX(t, f1,
		[]string{"编号", "姓名", "年龄"},
		[][]string{
			{"1", "张三", "18"},
			{"2", "李四", "20"},
		},
	)
	// file2: change age for key=1, add key=3, remove key=2
	writeXLSX(t, f2,
		[]string{"编号", "姓名", "年龄"},
		[][]string{
			{"1", "张三", "19"},
			{"3", "王五", "22"},
		},
	)

	if err := GenerateCompareExportXLSX(f1, f2, "old.xlsx", "new.xlsx", out); err != nil {
		t.Fatalf("GenerateCompareExportXLSX err=%v", err)
	}
	of, err := excelize.OpenFile(out)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = of.Close() }()

	sheets := of.GetSheetList()
	if len(sheets) != 3 {
		t.Fatalf("expected 3 sheets, got=%v", sheets)
	}
	// Order: 增加 / 减少 / 变动项目
	if sheets[0] != "new相比old增加" || sheets[1] != "new相比old减少" || sheets[2] != "变动项目" {
		t.Fatalf("unexpected sheet names: %v", sheets)
	}

	// 增加项: should contain key=3
	v, _ := of.GetCellValue(sheets[0], "A2")
	if v != "编号" {
		// Our simple table writer writes header at row1 only if non-empty; increased has rows, so A1 is header.
		v, _ = of.GetCellValue(sheets[0], "A1")
		if v != "编号" {
			t.Fatalf("expected header 编号, got %q", v)
		}
	}
	// Data row should include key=3 (second row after header)
	key3, _ := of.GetCellValue(sheets[0], "A2")
	if key3 != "3" {
		t.Fatalf("expected increased key 3 at A2, got=%q", key3)
	}

	// 变动项目: age cell pair should be styled (different).
	// Header layout: A=编号, B=姓名(file1), C=姓名(file2), D=年龄(file1), E=年龄(file2)
	// First diff row (key=1) is row2. Age cells are D2/E2.
	styleD2, _ := of.GetCellStyle(sheets[2], "D2")
	styleE2, _ := of.GetCellStyle(sheets[2], "E2")
	if styleD2 == 0 || styleE2 == 0 || styleD2 != styleE2 {
		t.Fatalf("expected red style on D2/E2, got styleD2=%d styleE2=%d", styleD2, styleE2)
	}
	styleB2, _ := of.GetCellStyle(sheets[2], "B2")
	if styleB2 == styleD2 {
		t.Fatalf("expected non-diff cell B2 to not share diff style")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool { return (stringIndex(s, sub) >= 0) })())
}

func stringIndex(s, sub string) int {
	// tiny strings.Index to avoid extra imports in tests
outer:
	for i := 0; i+len(sub) <= len(s); i++ {
		for j := 0; j < len(sub); j++ {
			if s[i+j] != sub[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}
