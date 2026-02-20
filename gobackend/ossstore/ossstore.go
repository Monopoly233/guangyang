package ossstore

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/aliyun/credentials-go/credentials"
)

type Store struct {
	bucketName string

	uploadBucket *oss.Bucket
	signBucket   *oss.Bucket

	prefix     string
	signExpiry time.Duration
}

func NewFromEnv() (*Store, bool, error) {
	bucket := strings.TrimSpace(os.Getenv("OSS_BUCKET"))
	if bucket == "" {
		return nil, false, nil
	}

	region := strings.TrimSpace(os.Getenv("OSS_REGION"))
	if region == "" {
		// 兼容：不填就不强制 region，但 AuthV4 需要 region。
		// 若你的 bucket 在国内大区，建议显式设置，比如 cn-heyuan。
		region = "cn-heyuan"
	}

	internalEndpoint := strings.TrimSpace(os.Getenv("OSS_ENDPOINT_INTERNAL"))
	publicEndpoint := strings.TrimSpace(os.Getenv("OSS_ENDPOINT_PUBLIC"))
	if internalEndpoint == "" && publicEndpoint == "" {
		return nil, true, errors.New("已设置 OSS_BUCKET，但缺少 OSS_ENDPOINT_INTERNAL/OSS_ENDPOINT_PUBLIC")
	}
	if publicEndpoint == "" {
		// 兜底：签名 URL 必须对浏览器可访问；若只填 internal，就会签出 internal 域名导致外网打不开。
		publicEndpoint = internalEndpoint
	}
	if internalEndpoint == "" {
		internalEndpoint = publicEndpoint
	}

	prefix := strings.TrimSpace(os.Getenv("OSS_PREFIX"))
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		prefix = "compare-results"
	}

	expirySec := readEnvInt64Default("OSS_SIGN_EXPIRE_SECONDS", 600)
	if expirySec <= 0 {
		expirySec = 600
	}

	cred, err := credentials.NewCredential(nil) // 支持：本地 AK、ACK RRSA(OIDC)、其他链路
	if err != nil {
		return nil, true, fmt.Errorf("init alibaba credentials failed: %w", err)
	}
	// 尽早校验一次，避免后续 PutObject 以“匿名请求”形式打到 OSS，导致 403 bucket acl 这种误导性错误。
	if err := validateAlibabaCredential(cred); err != nil {
		return nil, true, err
	}

	provider := &credentialsProvider{cred: cred}

	uploadClient, err := newOSSClient(internalEndpoint, region, provider)
	if err != nil {
		return nil, true, fmt.Errorf("init oss upload client failed: %w", err)
	}
	signClient, err := newOSSClient(publicEndpoint, region, provider)
	if err != nil {
		return nil, true, fmt.Errorf("init oss sign client failed: %w", err)
	}

	ub, err := uploadClient.Bucket(bucket)
	if err != nil {
		return nil, true, fmt.Errorf("open oss bucket(upload) failed: %w", err)
	}
	sb, err := signClient.Bucket(bucket)
	if err != nil {
		return nil, true, fmt.Errorf("open oss bucket(sign) failed: %w", err)
	}

	return &Store{
		bucketName:   bucket,
		uploadBucket: ub,
		signBucket:   sb,
		prefix:       prefix,
		signExpiry:   time.Duration(expirySec) * time.Second,
	}, true, nil
}

func validateAlibabaCredential(cred credentials.Credential) error {
	if cred == nil {
		return errors.New("阿里云凭证未初始化（RRSA/AK/STS 都不可用）")
	}
	c, err := cred.GetCredential()
	if err != nil {
		return fmt.Errorf("获取阿里云临时凭证失败（检查 RRSA 是否注入）：%w", err)
	}
	if c == nil || c.AccessKeyId == nil || c.AccessKeySecret == nil || strings.TrimSpace(*c.AccessKeyId) == "" || strings.TrimSpace(*c.AccessKeySecret) == "" {
		return errors.New("阿里云凭证为空：很可能 RRSA 未注入。请检查 Pod 内是否存在 ALIBABA_CLOUD_ROLE_ARN / ALIBABA_CLOUD_OIDC_PROVIDER_ARN / ALIBABA_CLOUD_OIDC_TOKEN_FILE")
	}
	return nil
}

func newOSSClient(endpoint, region string, provider oss.CredentialsProvider) (*oss.Client, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("endpoint empty")
	}
	opts := []oss.ClientOption{
		oss.SetCredentialsProvider(provider),
		oss.AuthVersion(oss.AuthV4),
	}
	if strings.TrimSpace(region) != "" {
		opts = append(opts, oss.Region(region))
	}
	// accessKeyId/secret 留空，完全走 provider（RRSA/AK/STS）。
	return oss.New(endpoint, "", "", opts...)
}

func (s *Store) Enabled() bool { return s != nil && s.uploadBucket != nil && s.signBucket != nil }

func (s *Store) ObjectKeyForJob(jobID string) string {
	jobID = strings.TrimSpace(jobID)
	return path.Join(s.prefix, jobID, "compare.xlsx")
}

func (s *Store) PutResultFile(objectKey, localPath string) error {
	if !s.Enabled() {
		return errors.New("oss not enabled")
	}
	objectKey = strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	localPath = strings.TrimSpace(localPath)
	if objectKey == "" || localPath == "" {
		return errors.New("invalid objectKey/localPath")
	}

	return s.uploadBucket.PutObjectFromFile(
		objectKey,
		localPath,
		oss.ContentType("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"),
	)
}

func (s *Store) GetObject(objectKey string) (io.ReadCloser, error) {
	if !s.Enabled() {
		return nil, errors.New("oss not enabled")
	}
	objectKey = strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	if objectKey == "" {
		return nil, errors.New("objectKey empty")
	}
	// 用 uploadBucket（通常指向 internal endpoint）拉取对象，避免出网带宽。
	return s.uploadBucket.GetObject(objectKey)
}

func (s *Store) SignDownloadURL(objectKey, downloadFilename string) (string, error) {
	if !s.Enabled() {
		return "", errors.New("oss not enabled")
	}
	objectKey = strings.TrimLeft(strings.TrimSpace(objectKey), "/")
	if objectKey == "" {
		return "", errors.New("objectKey empty")
	}

	name := strings.TrimSpace(downloadFilename)
	if name == "" {
		name = "比对结果.xlsx"
	}
	escaped := url.PathEscape(name)
	disp := fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", "compare.xlsx", escaped)

	u, err := s.signBucket.SignURL(
		objectKey,
		oss.HTTPGet,
		int64(s.signExpiry.Seconds()),
		oss.ResponseContentDisposition(disp),
		oss.ResponseContentType("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"),
	)
	if err != nil {
		return "", err
	}
	return u, nil
}

// --- Credentials bridge: credentials-go -> OSS SDK V1 ---

type credentialsProvider struct {
	cred credentials.Credential
}

type ossCred struct {
	AccessKeyId     string
	AccessKeySecret string
	SecurityToken   string
}

func (c *ossCred) GetAccessKeyID() string     { return c.AccessKeyId }
func (c *ossCred) GetAccessKeySecret() string { return c.AccessKeySecret }
func (c *ossCred) GetSecurityToken() string   { return c.SecurityToken }

func (p *credentialsProvider) GetCredentials() oss.Credentials {
	out, err := p.cred.GetCredential()
	if err != nil || out == nil || out.AccessKeyId == nil || out.AccessKeySecret == nil {
		// OSS SDK V1 的 provider 接口不返回 error；这里返回空凭证，让请求在调用时失败并暴露错误。
		return &ossCred{}
	}
	token := ""
	if out.SecurityToken != nil {
		token = *out.SecurityToken
	}
	return &ossCred{
		AccessKeyId:     deref(out.AccessKeyId),
		AccessKeySecret: deref(out.AccessKeySecret),
		SecurityToken:   token,
	}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func readEnvInt64Default(key string, def int64) int64 {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n
}

