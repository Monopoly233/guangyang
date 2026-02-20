package wechat

import (
	"errors"
	"fmt"
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
	_ = lastErr
	return "", fmt.Errorf("缺少 WECHAT_API_V3_KEY：请通过环境变量注入（如 `docker compose --env-file env.prod up -d`），或在 `wechatpay/apikey/apikey.txt` 中写入 `WECHAT_API_V3_KEY=...`")
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

// readWechatPayAppID is the appid used in WeChatPay v3 request body.
// For most scenarios this is the "wx..." appid (公众号/小程序/APP).
// If your deployment also uses enterprise WeCom corpId ("ww..."), keep it in WECHAT_CORP_ID
// and set WECHAT_PAY_APPID for payment, otherwise it may be rejected by WeChatPay API.
func readWechatPayAppID() string {
	if v := strings.TrimSpace(os.Getenv("WECHAT_PAY_APPID")); v != "" {
		return v
	}
	if v := inferFromApikeyFile("WECHAT_PAY_APPID"); v != "" {
		return v
	}
	// Backward compatible: fall back to WECHAT_APPID.
	return readWechatAppID()
}

func readWechatCorpID() string {
	if v := strings.TrimSpace(os.Getenv("WECHAT_CORP_ID")); v != "" {
		return v
	}
	if v := inferFromApikeyFile("WECHAT_CORP_ID"); v != "" {
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

// Platform verification config (either platform certificate or platform public key).
// If you configure platform public key mode, provide:
// - WECHAT_PLATFORM_PUBLIC_KEY_ID (value like PUB_KEY_ID_...)
// - WECHAT_PLATFORM_PUBLIC_KEY (PEM content; can be multi-line with \n escaped if needed)
func readWechatPlatformPublicKeyID() string {
	if v := strings.TrimSpace(os.Getenv("WECHAT_PLATFORM_PUBLIC_KEY_ID")); v != "" {
		return v
	}
	if v := inferFromApikeyFile("WECHAT_PLATFORM_PUBLIC_KEY_ID"); v != "" {
		return v
	}
	// Backward/legacy: some people store a naked "PUB_KEY_ID_..." line in apikey.txt.
	if v := inferPubKeyIDFromApikeyFile(); v != "" {
		return v
	}
	return ""
}

func readWechatPlatformPublicKeyPEM() string {
	if v := strings.TrimSpace(os.Getenv("WECHAT_PLATFORM_PUBLIC_KEY")); v != "" {
		// Guardrail: if the env exists but is clearly malformed/truncated (e.g. only BEGIN/END lines),
		// do not block file-based injection via wechatpay/cert/pub_key.pem.
		// A real RSA public key PEM is typically hundreds of bytes.
		if len(v) < 200 && strings.Contains(v, "BEGIN PUBLIC KEY") && strings.Contains(v, "END PUBLIC KEY") {
			// fall through to file-based lookup
		} else {
			return v
		}
	}
	if v := inferFromApikeyFile("WECHAT_PLATFORM_PUBLIC_KEY"); v != "" {
		return v
	}
	// Optional: allow mounting a pem file instead of embedding multi-line env.
	// Put it at: wechatpay/cert/platform_public_key.pem (or pub_key.pem for convenience)
	for _, p := range []string{
		filepath.Join("wechatpay", "cert", "platform_public_key.pem"),
		filepath.Join("wechatpay", "cert", "pub_key.pem"),
		filepath.Join("..", "wechatpay", "cert", "platform_public_key.pem"),
		filepath.Join("..", "wechatpay", "cert", "pub_key.pem"),
		filepath.Join(wechatpayCertCacheDir(), "platform_public_key.pem"),
		filepath.Join(wechatpayCertCacheDir(), "pub_key.pem"),
	} {
		if b, err := os.ReadFile(p); err == nil {
			if v := strings.TrimSpace(string(b)); v != "" {
				return v
			}
		}
	}
	return ""
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

func inferPubKeyIDFromApikeyFile() string {
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
		if v := inferPubKeyIDFromText(string(b)); v != "" {
			return v
		}
	}
	return ""
}

func inferPubKeyIDFromText(text string) string {
	for _, line := range strings.Split(text, "\n") {
		l := strings.TrimSpace(line)
		if l == "" {
			continue
		}
		// Accept either:
		// - "WECHAT_PLATFORM_PUBLIC_KEY_ID=PUB_KEY_ID_..."
		// - "PUB_KEY_ID_..." (naked value)
		if strings.Contains(l, "PUB_KEY_ID_") {
			// Extract the token starting from PUB_KEY_ID_
			idx := strings.Index(l, "PUB_KEY_ID_")
			if idx >= 0 {
				token := strings.TrimSpace(l[idx:])
				// stop at whitespace if any
				if fields := strings.Fields(token); len(fields) > 0 {
					return fields[0]
				}
				return token
			}
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

	for _, dir := range []string{
		filepath.Join("wechatpay", "cert"),
		filepath.Join("..", "wechatpay", "cert"),
	} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			m := re.FindStringSubmatch(name)
			if len(m) != 2 {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			cands = append(cands, candidate{mchID: m[1], mtime: info.ModTime()})
		}
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].mtime.After(cands[j].mtime) })
	if len(cands) > 0 {
		return cands[0].mchID
	}
	return ""
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
		// quick scan: allow "KEY=VALUE" or "KEY: VALUE"
		if v := parseKeyFromText(string(b), keyName); v != "" {
			return v
		}
	}
	return ""
}
