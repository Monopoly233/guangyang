package main

import (
	"errors"
	"os"
	"path/filepath"
)

func resolveWechatpayPlatformCertPath() (string, error) {
	candidates := []string{
		filepath.Join("wechatpay", "cert", "platform_cert.pem"),
		filepath.Join("..", "wechatpay", "cert", "platform_cert.pem"),
	}
	for _, p := range candidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errors.New("缺少平台证书文件 platform_cert.pem（需要从 cert.zip 解压/导出）")
}

