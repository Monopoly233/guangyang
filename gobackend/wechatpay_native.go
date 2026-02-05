package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// createWechatNativeOrder creates a Native pay order and returns code_url.
// MVP behavior:
// - If WECHAT_MOCK=1, returns a placeholder code_url for UI testing.
func createWechatNativeOrder(outTradeNo string, totalFen int64) (string, error) {
	if strings.TrimSpace(outTradeNo) == "" {
		return "", errors.New("out_trade_no 为空")
	}
	if totalFen <= 0 {
		return "", errors.New("金额必须为正数(分)")
	}

	if strings.TrimSpace(os.Getenv("WECHAT_MOCK")) == "1" {
		// A non-empty URL is enough for frontend QR rendering tests.
		return fmt.Sprintf("weixin://wxpay/bizpayurl?pr=%s", outTradeNo), nil
	}

	mchID := readWechatMchID()
	appID := readWechatAppID()
	notifyURL := readWechatNotifyURL()
	apiV3Key, err := readWechatAPIV3Key()
	if err != nil {
		return "", fmt.Errorf("读取 WECHAT_API_V3_KEY 失败: %w", err)
	}
	if mchID == "" {
		return "", errors.New("缺少 WECHAT_MCHID")
	}
	if appID == "" {
		return "", errors.New("缺少 WECHAT_APPID")
	}
	if notifyURL == "" {
		return "", errors.New("缺少 WECHAT_NOTIFY_URL")
	}

	merchantKeyPath, merchantCertPath, platformCertPath, err := resolveWechatpayCertPaths()
	if err != nil {
		return "", err
	}
	merchantPrivateKey, err := loadRSAPrivateKeyFromPath(merchantKeyPath)
	if err != nil {
		return "", fmt.Errorf("加载商户私钥失败: %w", err)
	}
	merchantCert, err := loadX509CertFromPath(merchantCertPath)
	if err != nil {
		return "", fmt.Errorf("加载商户证书失败: %w", err)
	}
	platformCert, err := loadX509CertFromPath(platformCertPath)
	if err != nil {
		return "", fmt.Errorf("加载平台证书失败: %w", err)
	}

	merchantSerial := strings.ToUpper(merchantCert.SerialNumber.Text(16))
	if merchantSerial == "" {
		return "", errors.New("无法获取商户证书序列号")
	}

	reqBody := map[string]interface{}{
		"appid":        appID,
		"mchid":        mchID,
		"description":  "Excel 对比导出",
		"out_trade_no": outTradeNo,
		"notify_url":   notifyURL,
		"amount": map[string]interface{}{
			"total":    totalFen,
			"currency": "CNY",
		},
	}
	bodyBytes, _ := json.Marshal(reqBody)

	// apiV3Key is used for notify decryption; prepay request itself doesn't use it.
	_ = apiV3Key
	codeURL, err := wechatpayPostNativePrepay(mchID, merchantSerial, merchantPrivateKey, platformCert, bodyBytes)
	if err != nil {
		return "", err
	}
	return codeURL, nil
}

func wechatpayPostNativePrepay(mchID, merchantSerial string, merchantPrivateKey *rsa.PrivateKey, platformCert *x509.Certificate, body []byte) (string, error) {
	endpoint := "https://api.mch.weixin.qq.com/v3/pay/transactions/native"
	u, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	u.Header.Set("Content-Type", "application/json")
	u.Header.Set("Accept", "application/json")

	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := mustNonce()
	signature, err := wechatpaySignRequest(merchantPrivateKey, http.MethodPost, "/v3/pay/transactions/native", timestamp, nonce, body)
	if err != nil {
		return "", err
	}
	authz := fmt.Sprintf(
		`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		mchID, nonce, timestamp, merchantSerial, signature,
	)
	u.Header.Set("Authorization", authz)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(respBody))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("微信预下单失败: %s", msg)
	}

	// Verify response signature (best-effort; if headers missing, fail closed).
	if err := verifyWechatpayResponseSignature(resp.Header, respBody, platformCert); err != nil {
		return "", fmt.Errorf("微信应答验签失败: %w", err)
	}

	var out struct {
		CodeURL string `json:"code_url"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.CodeURL) == "" {
		return "", errors.New("微信预下单未返回 code_url")
	}
	return out.CodeURL, nil
}

func wechatpaySignRequest(priv *rsa.PrivateKey, method, canonicalURL, timestamp, nonce string, body []byte) (string, error) {
	// message = method + "\n" + canonical_url + "\n" + timestamp + "\n" + nonce + "\n" + body + "\n"
	msg := method + "\n" + canonicalURL + "\n" + timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	h := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, h[:])
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(sig), nil
}

func verifyWechatpayResponseSignature(h http.Header, body []byte, platformCert *x509.Certificate) error {
	ts := h.Get("Wechatpay-Timestamp")
	nonce := h.Get("Wechatpay-Nonce")
	sigB64 := h.Get("Wechatpay-Signature")
	serial := h.Get("Wechatpay-Serial")
	if ts == "" || nonce == "" || sigB64 == "" || serial == "" {
		return errors.New("缺少微信应答验签头")
	}
	if platformCert == nil || platformCert.PublicKey == nil {
		return errors.New("平台证书无效")
	}
	platformSerial := strings.ToUpper(platformCert.SerialNumber.Text(16))
	if platformSerial != "" && strings.ToUpper(serial) != platformSerial {
		return fmt.Errorf("平台证书序列号不匹配: header=%s cert=%s", serial, platformSerial)
	}

	msg := ts + "\n" + nonce + "\n" + string(body) + "\n"
	hh := sha256.Sum256([]byte(msg))
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return err
	}
	pub, ok := platformCert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return errors.New("平台证书公钥不是 RSA")
	}
	return rsa.VerifyPKCS1v15(pub, crypto.SHA256, hh[:], sig)
}

func mustNonce() string {
	// 16 random bytes -> 32 hex chars
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		return hexLower(buf)
	}
	// fallback
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func hexLower(b []byte) string {
	const hextable = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hextable[v>>4]
		out[i*2+1] = hextable[v&0x0f]
	}
	return string(out)
}

func resolveWechatpayCertPaths() (merchantKeyPath, merchantCertPath, platformCertPath string, err error) {
	// fixed paths (repo root or gobackend/ as cwd)
	candidates := [][]string{
		{filepath.Join("wechatpay", "cert", "merchant_key.pem"), filepath.Join("wechatpay", "cert", "merchant_cert.pem"), filepath.Join("wechatpay", "cert", "platform_cert.pem")},
		{filepath.Join("..", "wechatpay", "cert", "merchant_key.pem"), filepath.Join("..", "wechatpay", "cert", "merchant_cert.pem"), filepath.Join("..", "wechatpay", "cert", "platform_cert.pem")},
	}
	for _, c := range candidates {
		if fileExists(c[0]) && fileExists(c[1]) && fileExists(c[2]) {
			return c[0], c[1], c[2], nil
		}
	}
	return "", "", "", errors.New("缺少证书文件：请在 wechatpay/cert/ 下放置 merchant_key.pem、merchant_cert.pem、platform_cert.pem")
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func loadRSAPrivateKeyFromPath(path string) (*rsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// support PKCS8 or PKCS1
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("无法解析 PEM")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	pk, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := pk.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("私钥不是 RSA")
	}
	return rk, nil
}

func loadX509CertFromPath(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("无法解析 PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

