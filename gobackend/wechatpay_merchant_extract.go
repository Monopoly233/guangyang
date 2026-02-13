package main

import (
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func resolveMerchantCertKeyPaths() (keyPath string, certPath string, err error) {
	// 1) Prefer explicit pem names
	candidates := [][]string{
		{filepath.Join("wechatpay", "cert", "merchant_key.pem"), filepath.Join("wechatpay", "cert", "merchant_cert.pem")},
		{filepath.Join("..", "wechatpay", "cert", "merchant_key.pem"), filepath.Join("..", "wechatpay", "cert", "merchant_cert.pem")},
		{filepath.Join("wechatpay", "cert", "apiclient_key.pem"), filepath.Join("wechatpay", "cert", "apiclient_cert.pem")},
		{filepath.Join("..", "wechatpay", "cert", "apiclient_key.pem"), filepath.Join("..", "wechatpay", "cert", "apiclient_cert.pem")},
	}
	for _, c := range candidates {
		if fileExists(c[0]) && fileExists(c[1]) {
			return c[0], c[1], nil
		}
	}

	// 2) Fallback: extract from *_cert.zip (the user has this file)
	zipPath, err := findMerchantCertZip()
	if err != nil {
		return "", "", err
	}

	tmpRoot := readEnvDefault("TMP_ROOT", "./tmp")
	outDir := filepath.Join(tmpRoot, "wechatpay_extracted", strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath)))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", "", err
	}

	keyOut := filepath.Join(outDir, "apiclient_key.pem")
	certOut := filepath.Join(outDir, "apiclient_cert.pem")

	// extraction is idempotent
	if !(fileExists(keyOut) && fileExists(certOut)) {
		if err := extractFilesFromZip(zipPath, map[string]string{
			"apiclient_key.pem":  keyOut,
			"apiclient_cert.pem": certOut,
		}); err != nil {
			return "", "", err
		}
	}

	if !fileExists(keyOut) || !fileExists(certOut) {
		return "", "", errors.New("商户证书/私钥解压失败：未找到 apiclient_key.pem 或 apiclient_cert.pem")
	}
	return keyOut, certOut, nil
}

func findMerchantCertZip() (string, error) {
	// Match pattern: <mchid>_YYYYMMDD_cert.zip
	re := regexp.MustCompile(`^\d+_\d{8}_cert\.zip$`)
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
			if re.MatchString(ent.Name()) {
				return filepath.Join(dir, ent.Name()), nil
			}
		}
	}
	return "", errors.New("缺少商户证书 zip：请将 <mchid>_YYYYMMDD_cert.zip 放到 wechatpay/cert/")
}

func extractFilesFromZip(zipPath string, nameToOutPath map[string]string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer r.Close()

	needed := map[string]bool{}
	for name := range nameToOutPath {
		needed[name] = false
	}

	for _, f := range r.File {
		base := filepath.Base(f.Name)
		out, ok := nameToOutPath[base]
		if !ok {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		if err := writeFileAtomic(out, rc, 0o600); err != nil {
			_ = rc.Close()
			return err
		}
		_ = rc.Close()
		needed[base] = true
	}

	for k, ok := range needed {
		if !ok {
			return errors.New("zip 内缺少文件: " + k)
		}
	}
	return nil
}

func writeFileAtomic(path string, r io.Reader, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, path)
}

