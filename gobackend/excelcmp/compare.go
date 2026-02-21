package excelcmp

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var primaryKeyCandidates = []string{"id", "编号", "编码", "资产编号", "资产编码", "序号", "资产号", "code", "no", "序列号"}

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
	ReducedKeys []string // file1-only keys (sorted)
	IncKeys     []string // file2-only keys (sorted)
	RedHeaders  []string // file1 headers order
	IncHeaders  []string // file2 headers order
	OrderedCols []string

	// Common keys between file1/file2 (sorted). Export will stream diff rows by scanning this list.
	CommonKeys []string

	// Export-time access: keep references to the original key->row maps and column index mapping.
	LeftByKey  map[string][]string // key -> original row values (file1 headers order)
	RightByKey map[string][]string // key -> original row values (file2 headers order)
	ColIdx1    []int               // aligned with OrderedCols: index into file1 row (or -1)
	ColIdx2    []int               // aligned with OrderedCols: index into file2 row (or -1)
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
	return compareArtifactsFromMaps(file1.Headers, file2.Headers, m1, m2, key)
}

func compareArtifactsFromMaps(headers1, headers2 []string, m1, m2 map[string][]string, key string) (*Artifacts, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("主键列为空")
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

	orderedCols := orderedUnionCols(headers1, headers2, key)

	hidx1 := headerIndexMap(headers1)
	hidx2 := headerIndexMap(headers2)
	colIdx1, colIdx2 := alignedColumnIndices(orderedCols, hidx1, hidx2)

	art := &Artifacts{
		Key:         key,
		ReducedKeys: only1,
		IncKeys:     only2,
		RedHeaders:  append([]string(nil), headers1...),
		IncHeaders:  append([]string(nil), headers2...),
		OrderedCols: orderedCols,
		CommonKeys:  common,
		LeftByKey:   m1,
		RightByKey:  m2,
		ColIdx1:     colIdx1,
		ColIdx2:     colIdx2,
	}

	return art, nil
}

func diffMaskWords(nCols int) int {
	if nCols <= 0 {
		return 0
	}
	return (nCols + 63) / 64
}

func diffMaskSet(mask []uint64, i int) {
	if i < 0 {
		return
	}
	w := i >> 6
	b := uint(i & 63)
	if w < 0 || w >= len(mask) {
		return
	}
	mask[w] |= 1 << b
}

func diffMaskGet(mask []uint64, i int) bool {
	if i < 0 {
		return false
	}
	w := i >> 6
	b := uint(i & 63)
	if w < 0 || w >= len(mask) {
		return false
	}
	return (mask[w]&(1<<b) != 0)
}

func fingerprint64(s string) uint64 {
	// Deterministic, zero-allocation FNV-1a (64-bit).
	// Collisions are handled by the n1!=n2 fallback.
	var h uint64 = 14695981039346656037
	const prime uint64 = 1099511628211
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime
	}
	return h
}

func indexOfHeader(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
}

func headerIndexMap(headers []string) map[string]int {
	m := make(map[string]int, len(headers))
	for i, h := range headers {
		m[h] = i
	}
	return m
}

func alignedColumnIndices(cols []string, hidx1, hidx2 map[string]int) ([]int, []int) {
	i1 := make([]int, len(cols))
	i2 := make([]int, len(cols))
	for i, c := range cols {
		if v, ok := hidx1[c]; ok {
			i1[i] = v
		} else {
			i1[i] = -1
		}
		if v, ok := hidx2[c]; ok {
			i2[i] = v
		} else {
			i2[i] = -1
		}
	}
	return i1, i2
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
		// Row is immutable; store directly to avoid extra allocations.
		out[k] = row
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
			if len(row) == len(out.Headers) {
				out.Rows = append(out.Rows, row)
				continue
			}
			// Fallback padding (shouldn't happen for xlsx reader).
			cp := make([]string, len(out.Headers))
			copy(cp, row)
			out.Rows = append(out.Rows, cp)
		}
	}
	return out
}

func buildSubTableFromMap(headers []string, keys []string, m map[string][]string) *Table {
	out := &Table{Headers: append([]string(nil), headers...)}
	if len(keys) == 0 {
		return out
	}
	out.Rows = make([][]string, 0, len(keys))
	for _, k := range keys {
		if row, ok := m[k]; ok {
			if len(row) == len(out.Headers) {
				out.Rows = append(out.Rows, row)
			} else {
				cp := make([]string, len(out.Headers))
				copy(cp, row)
				out.Rows = append(out.Rows, cp)
			}
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
