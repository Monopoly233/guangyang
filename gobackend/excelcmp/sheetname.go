package excelcmp

import (
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

func sheetBaseName(filename string) string {
	name := strings.TrimSpace(filename)
	if name == "" {
		return "文件"
	}
	name = filepath.Base(name)
	if dot := strings.LastIndex(name, "."); dot > 0 {
		name = name[:dot]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "文件"
	}
	return name
}

func safeSheetName(name string) string {
	s := strings.TrimSpace(name)
	if s == "" {
		s = "Sheet"
	}
	// Excel forbids : \ / ? * [ ]
	for _, ch := range []string{":", "\\", "/", "?", "*", "[", "]"} {
		s = strings.ReplaceAll(s, ch, "_")
	}
	if utf8.RuneCountInString(s) > 31 {
		s = trimRunes(s, 31)
	}
	return s
}

func uniqueSheetName(name string, used map[string]struct{}) string {
	base := safeSheetName(name)
	cand := base
	i := 2
	for {
		if _, ok := used[cand]; !ok {
			used[cand] = struct{}{}
			return cand
		}
		suffix := "_" + strconv.Itoa(i)
		maxLen := 31 - utf8.RuneCountInString(suffix)
		if maxLen < 1 {
			maxLen = 1
		}
		cand = base
		if utf8.RuneCountInString(cand) > maxLen {
			cand = trimRunes(cand, maxLen)
		}
		cand = cand + suffix
		i++
	}
}

func trimRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	out := make([]rune, 0, n)
	for _, r := range s {
		out = append(out, r)
		if len(out) >= n {
			break
		}
	}
	return string(out)
}
