package domain

import "time"

type CompareJobStatus string

const (
	CompareJobStatusProcessing      CompareJobStatus = "processing"
	CompareJobStatusAwaitingPayment CompareJobStatus = "awaiting_payment"
	CompareJobStatusReady           CompareJobStatus = "ready"
	CompareJobStatusFailed          CompareJobStatus = "failed"
	CompareJobStatusCancelled       CompareJobStatus = "cancelled"
)

type CompareJob struct {
	ID        string           `json:"jobId"`
	Status    CompareJobStatus `json:"status"`
	CreatedAt time.Time        `json:"createdAt"`

	// Inputs (saved on disk)
	File1Path string `json:"-"`
	File2Path string `json:"-"`
	// Inputs (saved on OSS; required for compare-worker to fetch)
	File1OSSKey string `json:"-"`
	File2OSSKey string `json:"-"`
	// Original upload filenames (for export sheet names / headers)
	File1Name string `json:"-"`
	File2Name string `json:"-"`

	// Result (saved on disk or OSS)
	ResultPath string `json:"-"`
	// ResultOSSKey is the OSS object key (bucket is configured separately).
	ResultOSSKey string `json:"-"`

	// Payment gating
	AmountYuan  float64    `json:"amount,omitempty"` // 单位：元（AwaitingPayment 时返回给前端展示）
	CodeURL     string     `json:"code_url,omitempty"`
	Paid        bool       `json:"paid"`
	PaidAt      *time.Time `json:"paidAt,omitempty"`
	CancelledAt *time.Time `json:"cancelledAt,omitempty"`

	// Diagnostics (non-sensitive)
	Error string `json:"error,omitempty"`
}
