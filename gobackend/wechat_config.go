package main

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

func readWechatAPIV3Key() (string, error) {
	// Prefer env for deployment, fallback to fixed file paths for local dev.
	if v := strings.TrimSpace(os.Getenv("WECHAT_API_V3_KEY")); v != "" {
		return v, nil
	}

	// User currently stores it in wechatpay/apikey/apikey.txt (same file may also contain PUB_KEY_ID).
	candidates := []string{
		filepath.Join("wechatpay", "apikey", "apikey.txt"),
		filepath.Join("..", "wechatpay", "apikey", "apikey.txt"),
		filepath.Join("wechatpay", "apikey", "key.txt"),
		filepath.Join("..", "wechatpay", "apikey", "key.txt"),
	}

	var lastErr error
	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			lastErr = err
			continue
		}
		key := parseKeyFromText(string(b), "WECHAT_API_V3_KEY")
		if key != "" {
			return key, nil
		}
		lastErr = errors.New("WECHAT_API_V3_KEY not found in " + p)
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", errors.New("missing WECHAT_API_V3_KEY")
}

func readWechatAppID() string {
	if v := strings.TrimSpace(os.Getenv("WECHAT_APPID")); v != "" {
		return v
	}
	if v := inferFromApikeyFile("WECHAT_APPID"); v != "" {
		return v
	}
	return ""
}

func readWechatMchID() string {
	if v := strings.TrimSpace(os.Getenv("WECHAT_MCHID")); v != "" {
		return v
	}
	if v := inferMchIDFromCertZip(); v != "" {
		return v
	}
	return ""
}

func readWechatNotifyURL() string {
	return strings.TrimSpace(os.Getenv("WECHAT_NOTIFY_URL"))
}

func parseKeyFromText(text string, keyName string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		// Allow formats:
		// WECHAT_API_V3_KEY=xxx
		// WECHAT_API_V3_KEY = xxx
		// WECHAT_API_V3_KEY: xxx
		if strings.HasPrefix(l, keyName) {
			rest := strings.TrimSpace(strings.TrimPrefix(l, keyName))
			rest = strings.TrimSpace(strings.TrimPrefix(rest, "="))
			rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

func inferMchIDFromCertZip() string {
	// Expected filename pattern: <mchid>_<yyyymmdd>_cert.zip
	// Example in this repo: wechatpay/cert/1106035691_20260131_cert.zip
	re := regexp.MustCompile(`^(\d+)_\d{8}_cert\.zip$`)

	type candidate struct {
		mchID string
		mtime time.Time
	}
	var cands []candidate

	dirs := []string{
		filepath.Join("wechatpay", "cert"),
		filepath.Join("..", "wechatpay", "cert"),
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			m := re.FindStringSubmatch(name)
			if len(m) != 2 {
				continue
			}
			info, err := ent.Info()
			if err != nil {
				continue
			}
			cands = append(cands, candidate{
				mchID: m[1],
				mtime: info.ModTime(),
			})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	// Pick the newest one.
	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	return cands[0].mchID
}

func inferFromApikeyFile(keyName string) string {
	candidates := []string{
		filepath.Join("wechatpay", "apikey", "apikey.txt"),
		filepath.Join("..", "wechatpay", "apikey", "apikey.txt"),
		filepath.Join("wechatpay", "apikey", "key.txt"),
		filepath.Join("..", "wechatpay", "apikey", "key.txt"),
	}

	for _, p := range candidates {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if v := parseKeyFromText(string(b), keyName); v != "" {
			return v
		}
	}
	return ""
}

