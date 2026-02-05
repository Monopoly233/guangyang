package main

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type wechatpayNotifyEnvelope struct {
	Resource struct {
		Algorithm      string `json:"algorithm"`
		Ciphertext     string `json:"ciphertext"`
		AssociatedData string `json:"associated_data"`
		Nonce          string `json:"nonce"`
		OriginalType   string `json:"original_type"`
	} `json:"resource"`
}

type wechatpayTransaction struct {
	OutTradeNo  string `json:"out_trade_no"`
	TradeState  string `json:"trade_state"`
	SuccessTime string `json:"success_time"`
	Amount      struct {
		Total int64 `json:"total"`
	} `json:"amount"`
}

func (s *CompareService) registerWechatpayRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/wechatpay/notify", s.handleWechatpayNotify)
}

func (s *CompareService) handleWechatpayNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "read body failed"})
		return
	}

	apiV3Key, err := readWechatAPIV3Key()
	if err != nil {
		log.Printf("wechatpay notify: read apiV3Key error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "FAIL", "message": "server config error"})
		return
	}

	platformCertPath, err := resolveWechatpayPlatformCertPath()
	if err != nil {
		log.Printf("wechatpay notify: platform cert missing: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "FAIL", "message": "server config error"})
		return
	}
	platformCert, err := loadX509CertFromPath(platformCertPath)
	if err != nil {
		log.Printf("wechatpay notify: load platform cert error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "FAIL", "message": "server config error"})
		return
	}

	if err := verifyWechatpayRequestSignature(r.Header, body, platformCert); err != nil {
		log.Printf("wechatpay notify: signature verify failed: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "invalid signature"})
		return
	}

	var env wechatpayNotifyEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "invalid json"})
		return
	}

	plain, err := decryptWechatpayResource(apiV3Key, env.Resource.AssociatedData, env.Resource.Nonce, env.Resource.Ciphertext)
	if err != nil {
		log.Printf("wechatpay notify: decrypt failed: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "decrypt failed"})
		return
	}

	var tx wechatpayTransaction
	if err := json.Unmarshal(plain, &tx); err != nil {
		log.Printf("wechatpay notify: unmarshal tx failed: %v", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "invalid payload"})
		return
	}

	jobID := strings.TrimSpace(tx.OutTradeNo)
	if jobID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "missing out_trade_no"})
		return
	}

	if strings.ToUpper(tx.TradeState) != "SUCCESS" {
		// 非成功状态也返回 SUCCESS，避免微信重试淹没；商户侧可主动查询订单状态。
		writeJSON(w, http.StatusOK, map[string]string{"code": "SUCCESS", "message": "OK"})
		return
	}

	// 金额校验：本项目测试固定 0.01 元 (=1 分)
	if tx.Amount.Total != 1 {
		log.Printf("wechatpay notify: amount mismatch out_trade_no=%s total=%d", jobID, tx.Amount.Total)
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "amount mismatch"})
		return
	}

	update := func() {
		now := time.Now()
		j, ok := s.store.update(jobID, func(j *CompareJob) {
			// 幂等：已支付就不重复写
			if j.Paid {
				return
			}
			j.Paid = true
			j.PaidAt = &now
			// 如果结果已生成，则放行；否则等待 worker 完成后再放行
			if j.ResultPath != "" && (j.Status == CompareJobStatusAwaitingPayment || j.Status == CompareJobStatusProcessing) {
				j.Status = CompareJobStatusReady
			}
		})
		if !ok {
			log.Printf("wechatpay notify: job not found out_trade_no=%s", jobID)
		} else {
			_ = j
		}
	}

	if s.queue != nil {
		s.queue.Enqueue(update)
	} else {
		update()
	}

	writeJSON(w, http.StatusOK, map[string]string{"code": "SUCCESS", "message": "OK"})
}

func verifyWechatpayRequestSignature(h http.Header, body []byte, platformCert *x509.Certificate) error {
	ts := h.Get("Wechatpay-Timestamp")
	nonce := h.Get("Wechatpay-Nonce")
	sigB64 := h.Get("Wechatpay-Signature")
	serial := h.Get("Wechatpay-Serial")
	if ts == "" || nonce == "" || sigB64 == "" || serial == "" {
		return errors.New("缺少微信回调验签头")
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

