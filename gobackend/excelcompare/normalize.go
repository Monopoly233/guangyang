package excelcompare

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var rePureNumber = regexp.MustCompile(`^[+-]?\d+(\.\d+)?$`)

// NormalizeScalarForCompare converts a cell value (usually string) into a stable
// representation for comparison and display.
//
// Goals:
// - empty/whitespace -> ""
// - "1.0" -> "1"
// - trim spaces
// - keep reasonable fidelity for non-numeric strings
func NormalizeScalarForCompare(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	ls := strings.ToLower(s)
	if ls == "nan" || ls == "nat" || ls == "none" {
		return ""
	}

	// Try number normalization.
	// Accuracy rule: if it's a pure integer with leading zeros (e.g. "0001"),
	// treat it as a string (common for codes/ids), do NOT normalize to number.
	if rePureNumber.MatchString(s) {
		noSign := s
		if strings.HasPrefix(noSign, "+") || strings.HasPrefix(noSign, "-") {
			noSign = noSign[1:]
		}
		if strings.IndexByte(noSign, '.') < 0 {
			// integer
			if len(noSign) > 1 && strings.HasPrefix(noSign, "0") {
				return s
			}
		}

		if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			if float64(int64(f)) == f {
				return strconv.FormatInt(int64(f), 10)
			}
			// "g" keeps it compact while stable enough for our use.
			return strconv.FormatFloat(f, 'g', -1, 64)
		}
	}

	// Common date formats: keep as-is, but trim.
	// If it's RFC3339-ish, normalize to a stable layout.
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		// if only date, keep date.
		if t.Hour() == 0 && t.Minute() == 0 && t.Second() == 0 && t.Nanosecond() == 0 {
			return t.Format("2006-01-02")
		}
		return t.Format("2006-01-02 15:04:05")
	}

	return s
}

