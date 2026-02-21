package wechat

import (
	"encoding/json"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"gobackend/domain"
	"gobackend/store"
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

func RegisterNotifyRoutes(mux *http.ServeMux, st store.CompareJobStore) {
	h := &notifyHandler{store: st}
	mux.HandleFunc("/wechatpay/notify", h.handle)
	// 兼容末尾多一个 "/" 的 notify_url（Go 的 ServeMux 对不带 "/" 结尾的 pattern 是精确匹配）
	mux.HandleFunc("/wechatpay/notify/", h.handle)
}

type notifyHandler struct {
	store store.CompareJobStore
}

func (h *notifyHandler) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	log.Printf("wechatpay notify: hit path=%s", r.URL.Path)

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

	verifier, err := loadWechatpayVerifier()
	if err != nil {
		log.Printf("wechatpay notify: platform verifier error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "FAIL", "message": "server config error"})
		return
	}

	if err := verifier.Verify(r.Header, body); err != nil {
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

	// 金额校验：以 job 上记录的 amount 为准（单位：分）。若未记录则回退为 1 分（兼容旧逻辑/竞态）。
	if job, ok, _ := h.store.Get(jobID); ok {
		expectedFen := int64(math.Round(job.AmountYuan * 100))
		if expectedFen <= 0 {
			expectedFen = 1
		}
		if tx.Amount.Total != expectedFen {
			log.Printf("wechatpay notify: amount mismatch out_trade_no=%s expected=%d total=%d", jobID, expectedFen, tx.Amount.Total)
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "FAIL", "message": "amount mismatch"})
			return
		}
	}

	update := func() {
		now := time.Now()
		j, ok, _ := h.store.Update(jobID, func(j *domain.CompareJob) {
			// 幂等：已支付就不重复写
			if j.Paid {
				return
			}
			j.Paid = true
			j.PaidAt = &now
			// 如果结果已生成，则放行；否则先退出“等待支付”，继续轮询直到 ready。
			if hasResult(j) && (j.Status == domain.CompareJobStatusAwaitingPayment || j.Status == domain.CompareJobStatusProcessing) {
				j.Status = domain.CompareJobStatusReady
				j.AmountYuan = 0
				j.CodeURL = ""
				return
			}
			if j.Status == domain.CompareJobStatusAwaitingPayment {
				j.Status = domain.CompareJobStatusProcessing
			}
		})
		if !ok {
			log.Printf("wechatpay notify: job not found out_trade_no=%s", jobID)
		} else {
			_ = j
		}
	}
	update()

	writeJSON(w, http.StatusOK, map[string]string{"code": "SUCCESS", "message": "OK"})
}

func hasResult(job *domain.CompareJob) bool {
	if job == nil {
		return false
	}
	return strings.TrimSpace(job.ResultOSSKey) != "" || strings.TrimSpace(job.ResultPath) != ""
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
