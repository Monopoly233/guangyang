package excelcmp

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var primaryKeyCandidates = []string{"id", "编号", "编码", "资产编号", "序号", "资产号", "code", "no", "序列号"}

// GuessPrimaryKeyColumn replicates python `guess_primary_key_column`.
func GuessPrimaryKeyColumn(tbl *Table, checkRows int) (string, bool) {
	if tbl == nil || len(tbl.Headers) == 0 {
		return "", false
	}
	if checkRows <= 0 {
		checkRows = 5
	}

	bestCol := ""
	bestScore := -1

	for colIdx, colName := range tbl.Headers {
		// gather first N values
		n := checkRows
		if len(tbl.Rows) < n {
			n = len(tbl.Rows)
		}
		if n == 0 {
			continue
		}
		values := make([]string, 0, n)
		hasNull := false
		for i := 0; i < n; i++ {
			if colIdx >= len(tbl.Rows[i]) {
				hasNull = true
				break
			}
			v := strings.TrimSpace(tbl.Rows[i][colIdx])
			if v == "" {
				hasNull = true
				break
			}
			values = append(values, v)
		}
		if hasNull {
			continue
		}
		uniq := make(map[string]struct{}, len(values))
		for _, v := range values {
			if _, ok := uniq[v]; ok {
				hasNull = true // reuse flag: invalid due to duplicate
				break
			}
			uniq[v] = struct{}{}
		}
		if hasNull {
			continue
		}

		score := 0
		lcName := strings.ToLower(colName)
		for _, kw := range primaryKeyCandidates {
			if strings.Contains(lcName, strings.ToLower(kw)) {
				score += 10
			}
		}
		// pkish: integer-like OR alnum unicode
		pkishAll := true
		for _, v := range values {
			if isIntegerLikeString(v) || isFiniteIntegerFloatString(v) || isAlnumUnicode(v) {
				continue
			}
			pkishAll = false
			break
		}
		if pkishAll {
			score += 5
		}
		if score > bestScore {
			bestScore = score
			bestCol = colName
		}
	}
	if bestCol == "" {
		return "", false
	}
	return bestCol, true
}

type Artifacts struct {
	Key         string
	Reduced     *Table // file1-only, headers in file1 order
	Increased   *Table // file2-only, headers in file2 order
	OrderedCols []string

	// Only differing keys (normalized key as string)
	DiffKeys  []string
	LeftRows  map[string]map[string]string // key -> col -> value (ordered cols)
	RightRows map[string]map[string]string // key -> col -> value
	DiffMask  map[string]map[string]bool   // key -> col -> isDiff
}

func CompareArtifacts(file1, file2 *Table, key string) (*Artifacts, error) {
	if file1 == nil || file2 == nil {
		return nil, errors.New("输入表为空")
	}
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("主键列为空")
	}
	k1 := indexOfHeader(file1.Headers, key)
	k2 := indexOfHeader(file2.Headers, key)
	if k1 < 0 || k2 < 0 {
		return nil, fmt.Errorf("Excel文件中必须同时包含%q列", key)
	}

	// Build key->row maps. Normalize key and drop empty keys.
	m1, dup1 := buildKeyRowMap(file1, k1)
	if len(dup1) > 0 {
		return nil, fmt.Errorf("文件1主键列“%s”存在重复值（示例: %v），请先去重或修正后再比对", key, dup1)
	}
	m2, dup2 := buildKeyRowMap(file2, k2)
	if len(dup2) > 0 {
		return nil, fmt.Errorf("文件2主键列“%s”存在重复值（示例: %v），请先去重或修正后再比对", key, dup2)
	}

	only1 := make([]string, 0)
	only2 := make([]string, 0)
	common := make([]string, 0)

	for k := range m1 {
		if _, ok := m2[k]; ok {
			common = append(common, k)
		} else {
			only1 = append(only1, k)
		}
	}
	for k := range m2 {
		if _, ok := m1[k]; !ok {
			only2 = append(only2, k)
		}
	}

	// pandas Index.difference / intersection are deterministic (sorted).
	sort.Strings(only1)
	sort.Strings(only2)
	sort.Strings(common)

	reduced := buildSubTable(file1, only1, m1)
	increased := buildSubTable(file2, only2, m2)

	orderedCols := orderedUnionCols(file1.Headers, file2.Headers, key)

	art := &Artifacts{
		Key:         key,
		Reduced:     reduced,
		Increased:   increased,
		OrderedCols: orderedCols,
		DiffKeys:    nil,
		LeftRows:    map[string]map[string]string{},
		RightRows:   map[string]map[string]string{},
		DiffMask:    map[string]map[string]bool{},
	}

	if len(common) == 0 {
		return art, nil
	}

	// Determine differing keys
	for _, k := range common {
		r1 := m1[k]
		r2 := m2[k]
		diffCols := make(map[string]bool, len(orderedCols))
		hasDiff := false
		left := make(map[string]string, len(orderedCols))
		right := make(map[string]string, len(orderedCols))
		for _, col := range orderedCols {
			i1 := indexOfHeader(file1.Headers, col)
			i2 := indexOfHeader(file2.Headers, col)
			var v1, v2 string
			if i1 >= 0 && i1 < len(r1) {
				v1 = r1[i1]
			}
			if i2 >= 0 && i2 < len(r2) {
				v2 = r2[i2]
			}
			left[col] = v1
			right[col] = v2

			n1 := normalizeScalarForCompare(v1)
			n2 := normalizeScalarForCompare(v2)
			isDiff := n1 != n2
			if isDiff {
				hasDiff = true
			}
			diffCols[col] = isDiff
		}
		if hasDiff {
			art.DiffKeys = append(art.DiffKeys, k)
			art.LeftRows[k] = left
			art.RightRows[k] = right
			art.DiffMask[k] = diffCols
		}
	}
	return art, nil
}

func indexOfHeader(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}

func buildKeyRowMap(tbl *Table, keyIdx int) (map[string][]string, []string) {
	out := make(map[string][]string, len(tbl.Rows))
	dups := make([]string, 0)
	seenDup := make(map[string]struct{})
	for _, row := range tbl.Rows {
		if keyIdx >= len(row) {
			continue
		}
		k := normalizeScalarForCompare(row[keyIdx])
		if strings.TrimSpace(k) == "" {
			continue
		}
		if _, ok := out[k]; ok {
			if _, already := seenDup[k]; !already {
				seenDup[k] = struct{}{}
				if len(dups) < 10 {
					dups = append(dups, k)
				}
			}
			continue
		}
		// Copy row to avoid aliasing.
		cp := make([]string, len(row))
		copy(cp, row)
		out[k] = cp
	}
	return out, dups
}

func buildSubTable(src *Table, keys []string, m map[string][]string) *Table {
	if src == nil {
		return &Table{}
	}
	out := &Table{Headers: append([]string(nil), src.Headers...)}
	if len(keys) == 0 {
		return out
	}
	out.Rows = make([][]string, 0, len(keys))
	for _, k := range keys {
		if row, ok := m[k]; ok {
			cp := make([]string, len(out.Headers))
			for i := 0; i < len(out.Headers); i++ {
				if i < len(row) {
					cp[i] = row[i]
				} else {
					cp[i] = ""
				}
			}
			out.Rows = append(out.Rows, cp)
		}
	}
	return out
}

func orderedUnionCols(h1, h2 []string, key string) []string {
	set := make(map[string]struct{}, len(h1)+len(h2))
	for _, c := range h1 {
		set[c] = struct{}{}
	}
	for _, c := range h2 {
		set[c] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for _, c := range h1 {
		if c == key {
			continue
		}
		if _, ok := set[c]; ok {
			out = append(out, c)
		}
	}
	seen := make(map[string]struct{}, len(out))
	for _, c := range out {
		seen[c] = struct{}{}
	}
	for _, c := range h2 {
		if c == key {
			continue
		}
		if _, ok := set[c]; !ok {
			continue
		}
		if _, ok := seen[c]; ok {
			continue
		}
		out = append(out, c)
		seen[c] = struct{}{}
	}
	return out
}
