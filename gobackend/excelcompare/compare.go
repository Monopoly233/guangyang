package excelcompare

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

type CompareResult struct {
	KeyCol string
	File1Headers []string
	File2Headers []string

	Added   [][]string // rows from file2 (include key col), in file2 column order
	Removed [][]string // rows from file1 (include key col), in file1 column order

	// ChangedKeys are the keys with differences.
	ChangedKeys []string
	// OrderedCols are columns excluding key, in "file1 order then file2 new cols order".
	OrderedCols []string
	// Left/right values for each ChangedKey, aligned to OrderedCols.
	Left  map[string][]string
	Right map[string][]string
	// DiffMask[key][i] indicates OrderedCols[i] differs.
	DiffMask map[string][]bool
}

var pkNameHints = []string{"id", "编号", "编码", "资产编号", "序号", "资产号", "code", "no", "序列号"}

func GuessPrimaryKeyColumn(headers []string) (string, error) {
	if len(headers) == 0 {
		return "", errors.New("表头为空，无法猜测主键")
	}
	// Prefer hinted names.
	for _, h := range headers {
		hl := strings.ToLower(strings.TrimSpace(h))
		for _, kw := range pkNameHints {
			if strings.Contains(hl, strings.ToLower(kw)) {
				return h, nil
			}
		}
	}
	// Fallback: first non-empty header.
	for _, h := range headers {
		if strings.TrimSpace(h) != "" {
			return h, nil
		}
	}
	return "", errors.New("无法猜测主键列")
}

func CompareTables(t1, t2 *Table, keyCol string) (*CompareResult, error) {
	if t1 == nil || t2 == nil {
		return nil, errors.New("表为空")
	}
	keyCol = strings.TrimSpace(keyCol)
	if keyCol == "" {
		return nil, errors.New("主键列为空")
	}
	k1 := indexOfHeader(t1.Headers, keyCol)
	k2 := indexOfHeader(t2.Headers, keyCol)
	if k1 < 0 || k2 < 0 {
		return nil, fmt.Errorf("两份表必须都包含主键列: %s", keyCol)
	}

	// Build ordered union columns (excluding key).
	orderedCols := make([]string, 0, len(t1.Headers)+len(t2.Headers))
	seen := map[string]bool{}
	for _, c := range t1.Headers {
		if c == keyCol {
			continue
		}
		if strings.TrimSpace(c) == "" || seen[c] {
			continue
		}
		seen[c] = true
		orderedCols = append(orderedCols, c)
	}
	for _, c := range t2.Headers {
		if c == keyCol {
			continue
		}
		if strings.TrimSpace(c) == "" || seen[c] {
			continue
		}
		seen[c] = true
		orderedCols = append(orderedCols, c)
	}

	// Key->row maps.
	m1 := map[string][]string{}
	m2 := map[string][]string{}

	if err := fillMapUnique(m1, t1, k1, "文件1", keyCol); err != nil {
		return nil, err
	}
	if err := fillMapUnique(m2, t2, k2, "文件2", keyCol); err != nil {
		return nil, err
	}

	// Added/Removed keys.
	var (
		addedKeys   []string
		removedKeys []string
		commonKeys  []string
	)
	for k := range m1 {
		if _, ok := m2[k]; !ok {
			removedKeys = append(removedKeys, k)
		} else {
			commonKeys = append(commonKeys, k)
		}
	}
	for k := range m2 {
		if _, ok := m1[k]; !ok {
			addedKeys = append(addedKeys, k)
		}
	}
	sort.Strings(addedKeys)
	sort.Strings(removedKeys)
	sort.Strings(commonKeys)

	added := make([][]string, 0, len(addedKeys))
	for _, k := range addedKeys {
		added = append(added, rowInOriginalOrder(t2, m2[k]))
	}
	removed := make([][]string, 0, len(removedKeys))
	for _, k := range removedKeys {
		removed = append(removed, rowInOriginalOrder(t1, m1[k]))
	}

	// Changed
	left := map[string][]string{}
	right := map[string][]string{}
	diffMask := map[string][]bool{}
	var changedKeys []string

	colIdx1 := headerIndexMap(t1.Headers)
	colIdx2 := headerIndexMap(t2.Headers)

	for _, k := range commonKeys {
		r1 := m1[k]
		r2 := m2[k]
		lv := make([]string, len(orderedCols))
		rv := make([]string, len(orderedCols))
		dm := make([]bool, len(orderedCols))

		anyDiff := false
		for i, c := range orderedCols {
			v1 := ""
			if idx, ok := colIdx1[c]; ok && idx >= 0 && idx < len(r1) {
				v1 = r1[idx]
			}
			v2 := ""
			if idx, ok := colIdx2[c]; ok && idx >= 0 && idx < len(r2) {
				v2 = r2[idx]
			}
			n1 := NormalizeScalarForCompare(v1)
			n2 := NormalizeScalarForCompare(v2)
			lv[i] = n1
			rv[i] = n2
			if n1 != n2 {
				dm[i] = true
				anyDiff = true
			}
		}
		if anyDiff {
			changedKeys = append(changedKeys, k)
			left[k] = lv
			right[k] = rv
			diffMask[k] = dm
		}
	}

	return &CompareResult{
		KeyCol:       keyCol,
		File1Headers: append([]string(nil), t1.Headers...),
		File2Headers: append([]string(nil), t2.Headers...),
		Added:        added,
		Removed:      removed,
		ChangedKeys:  changedKeys,
		OrderedCols:  orderedCols,
		Left:         left,
		Right:        right,
		DiffMask:     diffMask,
	}, nil
}

func indexOfHeader(headers []string, col string) int {
	col = strings.TrimSpace(col)
	for i, h := range headers {
		if strings.TrimSpace(h) == col {
			return i
		}
	}
	return -1
}

func headerIndexMap(headers []string) map[string]int {
	m := make(map[string]int, len(headers))
	for i, h := range headers {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if _, ok := m[h]; ok {
			continue
		}
		m[h] = i
	}
	return m
}

func fillMapUnique(dst map[string][]string, t *Table, keyIdx int, which, keyCol string) error {
	dups := make(map[string]int)
	for _, r := range t.Rows {
		if keyIdx >= len(r) {
			continue
		}
		k := NormalizeScalarForCompare(r[keyIdx])
		if k == "" {
			continue
		}
		if _, ok := dst[k]; ok {
			dups[k]++
			continue
		}
		dst[k] = r
	}
	if len(dups) > 0 {
		// report up to 10 examples
		ex := make([]string, 0, len(dups))
		for k := range dups {
			ex = append(ex, k)
		}
		sort.Strings(ex)
		if len(ex) > 10 {
			ex = ex[:10]
		}
		return fmt.Errorf("%s 主键列“%s”存在重复值（示例: %v），请先去重或修正后再比对", which, keyCol, ex)
	}
	return nil
}

func rowInOriginalOrder(t *Table, row []string) []string {
	// Return a copy to avoid surprises.
	out := make([]string, len(t.Headers))
	for i := range out {
		if i < len(row) {
			out[i] = NormalizeScalarForCompare(row[i])
		} else {
			out[i] = ""
		}
	}
	return out
}

