package excelcmp

import (
	"strconv"
	"strings"
	"unicode"
)

// normalizeScalarForCompare replicates python `_normalize_scalar_for_compare` semantics
// for the value types we observe after reading from xlsx via excelize.
//
// Important: we intentionally avoid aggressively parsing arbitrary strings into
// numbers/datetimes, because in Python a text cell "001" remains "001".
func normalizeScalarForCompare(v string) string {
	s := strings.TrimSpace(v)
	if s == "" {
		return ""
	}
	ls := strings.ToLower(s)
	if ls == "nan" || ls == "nat" || ls == "none" {
		return ""
	}
	return s
}

func isAlnumUnicode(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func isIntegerLikeString(s string) bool {
	if s == "" {
		return false
	}
	// allow leading +/- ? python `_pkish` accepts int; excel strings may carry "-1"
	if s[0] == '-' || s[0] == '+' {
		s = s[1:]
		if s == "" {
			return false
		}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isFiniteIntegerFloatString(s string) bool {
	// best-effort for values rendered like "1" / "1.0" / "1.00"
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return false
	}
	// check integer-ish without importing math: f == float64(int64(f)) may overflow, so limit.
	if f > 9e15 || f < -9e15 {
		return false
	}
	i := int64(f)
	return float64(i) == f
}
