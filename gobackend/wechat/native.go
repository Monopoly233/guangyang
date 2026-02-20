package wechat

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// CreateNativeOrder creates a Native pay order and returns code_url.
// MVP behavior:
// - If WECHAT_MOCK=1, returns a placeholder code_url for UI testing.
func CreateNativeOrder(outTradeNo string, totalFen int64) (string, error) {
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
	appID := readWechatPayAppID()
	notifyURL := readWechatNotifyURL()
	apiV3Key, err := readWechatAPIV3Key()
	if err != nil {
		return "", fmt.Errorf("读取 WECHAT_API_V3_KEY 失败: %w", err)
	}
	if mchID == "" {
		return "", errors.New("缺少 WECHAT_MCHID")
	}
	if !isValidWechatMchID(mchID) {
		return "", fmt.Errorf("WECHAT_MCHID 非法：%q（必须是纯数字直连商户号，不能用 1106xxxx 这种占位符）", mchID)
	}
	if appID == "" {
		return "", errors.New("缺少 WECHAT_PAY_APPID（或兼容读取 WECHAT_APPID）")
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
	_ = platformCertPath // platform verifier may use cert and/or public key
	verifier, err := loadWechatpayVerifier()
	if err != nil {
		return "", err
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
	codeURL, err := wechatpayPostNativePrepay(mchID, merchantSerial, merchantPrivateKey, verifier, bodyBytes)
	if err != nil {
		return "", err
	}
	return codeURL, nil
}

// CloseNativeOrder closes a Native pay order by out_trade_no.
// It is used when user cancels payment on our side.
func CloseNativeOrder(outTradeNo string) error {
	if strings.TrimSpace(outTradeNo) == "" {
		return errors.New("out_trade_no 为空")
	}

	if strings.TrimSpace(os.Getenv("WECHAT_MOCK")) == "1" {
		return nil
	}

	mchID := readWechatMchID()
	if mchID == "" {
		return errors.New("缺少 WECHAT_MCHID")
	}
	if !isValidWechatMchID(mchID) {
		return fmt.Errorf("WECHAT_MCHID 非法：%q（必须是纯数字直连商户号）", mchID)
	}

	merchantKeyPath, merchantCertPath, platformCertPath, err := resolveWechatpayCertPaths()
	if err != nil {
		return err
	}
	merchantPrivateKey, err := loadRSAPrivateKeyFromPath(merchantKeyPath)
	if err != nil {
		return fmt.Errorf("加载商户私钥失败: %w", err)
	}
	merchantCert, err := loadX509CertFromPath(merchantCertPath)
	if err != nil {
		return fmt.Errorf("加载商户证书失败: %w", err)
	}
	_ = platformCertPath // verifier may use cert and/or public key
	verifier, err := loadWechatpayVerifier()
	if err != nil {
		return err
	}

	merchantSerial := strings.ToUpper(merchantCert.SerialNumber.Text(16))
	if merchantSerial == "" {
		return errors.New("无法获取商户证书序列号")
	}

	return wechatpayPostCloseOrder(mchID, merchantSerial, merchantPrivateKey, verifier, outTradeNo)
}

func wechatpayPostNativePrepay(mchID, merchantSerial string, merchantPriv *rsa.PrivateKey, verifier *wechatpayVerifier, body []byte) (string, error) {
	u := "https://api.mch.weixin.qq.com/v3/pay/transactions/native"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := mustNonce()
	canonicalURL, _ := url.Parse(u)
	sig, err := wechatpaySignRequest(merchantPriv, http.MethodPost, canonicalURL.RequestURI(), ts, nonce, body)
	if err != nil {
		return "", err
	}
	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		mchID, nonce, ts, merchantSerial, sig)

	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", auth)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("微信预下单请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return "", fmt.Errorf("微信预下单失败: %s", msg)
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		CodeURL string `json:"code_url"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.CodeURL) == "" {
		return "", errors.New("微信预下单未返回 code_url")
	}
	_ = verifier
	return out.CodeURL, nil
}

func wechatpayPostCloseOrder(mchID, merchantSerial string, merchantPriv *rsa.PrivateKey, verifier *wechatpayVerifier, outTradeNo string) error {
	u := "https://api.mch.weixin.qq.com/v3/pay/transactions/out-trade-no/" + url.PathEscape(outTradeNo) + "/close"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	nonce := mustNonce()

	reqBody := map[string]string{"mchid": mchID}
	body, _ := json.Marshal(reqBody)

	canonicalURL, _ := url.Parse(u)
	sig, err := wechatpaySignRequest(merchantPriv, http.MethodPost, canonicalURL.RequestURI(), ts, nonce, body)
	if err != nil {
		return err
	}
	auth := fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`,
		mchID, nonce, ts, merchantSerial, sig)

	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", auth)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("微信关单请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("微信关单失败: %s", msg)
	}
	_ = verifier
	return nil
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
	return ensureWechatpayMerchantPemFiles()
}
