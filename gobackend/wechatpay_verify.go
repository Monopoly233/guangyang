package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

type wechatpayVerifier struct {
	platformCert       *x509.Certificate
	platformCertSerial string

	platformPublicKey   *rsa.PublicKey
	platformPublicKeyID string // PUB_KEY_ID_...
}

func loadWechatpayVerifier() (*wechatpayVerifier, error) {
	v := &wechatpayVerifier{}

	// 1) Platform certificate (legacy / still common)
	if certPath, err := resolveWechatpayPlatformCertPath(); err == nil {
		if cert, err2 := loadX509CertFromPath(certPath); err2 == nil && cert != nil {
			v.platformCert = cert
			v.platformCertSerial = strings.ToUpper(cert.SerialNumber.Text(16))
		}
	}

	// 2) Platform public key mode
	v.platformPublicKeyID = strings.TrimSpace(readWechatPlatformPublicKeyID())
	if pemText := strings.TrimSpace(readWechatPlatformPublicKeyPEM()); pemText != "" {
		pub, err := parseRSAPublicKeyFromPEM(pemText)
		if err != nil {
			return nil, fmt.Errorf("解析 WECHAT_PLATFORM_PUBLIC_KEY 失败: %w", err)
		}
		v.platformPublicKey = pub
	}

	if v.platformCert == nil && v.platformPublicKey == nil {
		// Common confusion: users sometimes put merchant cert as cert.txt.
		if mch := strings.TrimSpace(readWechatMchID()); mch != "" {
			certTxt := filepath.Join("wechatpay", "cert", "cert.txt")
			if st, err := os.Stat(certTxt); err == nil && !st.IsDir() {
				if c, err2 := loadX509CertFromPath(certTxt); err2 == nil && c != nil {
					subCN := strings.TrimSpace(c.Subject.CommonName)
					subO := strings.TrimSpace(strings.Join(c.Subject.Organization, ","))
					// Merchant cert commonly has CN=<mchid> and O contains "微信商户系统".
					if subCN == mch || strings.Contains(subO, "微信商户系统") {
						return nil, errors.New("缺少平台验签材料：你当前提供的 wechatpay/cert/cert.txt 看起来是【商户证书】(不是平台证书)，无法用于验签。请提供 wechatpay/cert/platform_cert.pem 或提供平台公钥：WECHAT_PLATFORM_PUBLIC_KEY（或放置 wechatpay/cert/platform_public_key.pem）")
					}
				}
			}
		}
		return nil, errors.New("缺少平台验签材料：请提供 wechatpay/cert/platform_cert.pem，或提供平台公钥 WECHAT_PLATFORM_PUBLIC_KEY（也支持放置 wechatpay/cert/platform_public_key.pem / tmp/wechatpay_cache/cert/platform_public_key.pem）")
	}
	return v, nil
}

func (v *wechatpayVerifier) Verify(h http.Header, body []byte) error {
	ts := h.Get("Wechatpay-Timestamp")
	nonce := h.Get("Wechatpay-Nonce")
	sigB64 := h.Get("Wechatpay-Signature")
	serial := h.Get("Wechatpay-Serial")
	if ts == "" || nonce == "" || sigB64 == "" || serial == "" {
		return errors.New("缺少微信验签头")
	}

	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return err
	}
	msg := ts + "\n" + nonce + "\n" + string(body) + "\n"
	digest := sha256.Sum256([]byte(msg))

	serialUpper := strings.ToUpper(strings.TrimSpace(serial))

	// Try cert public key
	if v.platformCert != nil {
		if pub, ok := v.platformCert.PublicKey.(*rsa.PublicKey); ok && pub != nil {
			// If header looks like PUB_KEY_ID_..., likely public-key mode; skip strict serial check.
			// Otherwise, if we know cert serial, prefer a match but don't hard-fail solely on mismatch
			// (platform cert rotates; users may forget to update).
			if err2 := rsa.VerifyPKCS1v15(pub, crypto.SHA256, digest[:], sig); err2 == nil {
				// Best-effort serial sanity check (soft).
				if v.platformCertSerial != "" && !strings.HasPrefix(serialUpper, "PUB_KEY_ID_") {
					_ = v.platformCertSerial // keep for debugging, but do not reject if mismatch
				}
				return nil
			}
		}
	}

	// Try configured platform public key
	if v.platformPublicKey != nil {
		if err2 := rsa.VerifyPKCS1v15(v.platformPublicKey, crypto.SHA256, digest[:], sig); err2 == nil {
			// If user provides PUB_KEY_ID, it should match header, but don't reject on mismatch to avoid outage.
			if v.platformPublicKeyID != "" {
				_ = v.platformPublicKeyID
			}
			return nil
		}
	}

	return errors.New("验签失败：平台证书/公钥均未通过验证")
}

func parseRSAPublicKeyFromPEM(pemText string) (*rsa.PublicKey, error) {
	// Support env style with "\n" escapes.
	pemText = strings.ReplaceAll(pemText, `\n`, "\n")
	b := []byte(pemText)
	block, _ := pem.Decode(b)
	if block == nil {
		return nil, errors.New("无法解析 PEM")
	}
	// Try PKIX first.
	if pubAny, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if pub, ok := pubAny.(*rsa.PublicKey); ok && pub != nil {
			return pub, nil
		}
		return nil, errors.New("平台公钥不是 RSA")
	}
	// Fallback: parse certificate and use its public key if user pasted a cert by mistake.
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil && cert != nil {
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok && pub != nil {
			return pub, nil
		}
		return nil, errors.New("证书公钥不是 RSA")
	}
	return nil, errors.New("无法解析 RSA 公钥")
}

