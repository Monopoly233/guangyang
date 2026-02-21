package wechat

import (
	"errors"
	"os"
	"path/filepath"
)

func resolveWechatpayPlatformCertPath() (string, error) {
	candidates := []string{
		filepath.Join("wechatpay", "cert", "platform_cert.pem"),
		filepath.Join("..", "wechatpay", "cert", "platform_cert.pem"),
		filepath.Join(wechatpayCertCacheDir(), "platform_cert.pem"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	// Try extracting from zip into cache, then re-check.
	// - First prefer merchant cert zip (<mchid>_YYYYMMDD_cert.zip)
	// - Then fallback to newest *.zip (platform materials may be packaged separately)
	tryZips := []string{}
	if zipPath := findLatestWechatpayCertZip(); zipPath != "" {
		tryZips = append(tryZips, zipPath)
	}
	if zipPath := findLatestWechatpayAnyZip(); zipPath != "" {
		// avoid duplicate
		dup := false
		for _, z := range tryZips {
			if z == zipPath {
				dup = true
				break
			}
		}
		if !dup {
			tryZips = append(tryZips, zipPath)
		}
	}

	for _, zipPath := range tryZips {
		cacheDir := wechatpayCertCacheDir()
		_ = os.MkdirAll(cacheDir, 0o755)
		_ = extractWechatpayCertZip(zipPath, cacheDir)
		if st, err := os.Stat(filepath.Join(cacheDir, "platform_cert.pem")); err == nil && !st.IsDir() {
			return filepath.Join(cacheDir, "platform_cert.pem"), nil
		}
	}
	return "", errors.New("缺少平台证书文件 platform_cert.pem（可放在 wechatpay/cert/ 下，或仅放置 *_cert.zip 让程序自动解压到 tmp 缓存）；也可改用平台公钥模式：配置 WECHAT_PLATFORM_PUBLIC_KEY（或放置 wechatpay/cert/platform_public_key.pem）")
}
