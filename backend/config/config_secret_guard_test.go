package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalConfig 生成一份只带必需项的配置，mode 由参数决定。
func minimalConfig(mode, paymentSecret string) string {
	return `
server:
  host: "0.0.0.0"
  port: 9090
  mode: "` + mode + `"
mysql:
  dsn: "test:test@tcp(localhost:3306)/testdb?charset=utf8mb4&parseTime=True"
jwt:
  secret: "test-secret-at-least-not-a-placeholder"
ticket_qr:
  secret: "test-ticket-qr-secret-value"
payment:
  provider: "sandbox"
  sandbox_secret: "` + paymentSecret + `"
`
}

func loadConfigWith(t *testing.T, content string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return Load(path)
}

func TestLoadRejectsPlaceholderPaymentSecretInReleaseMode(t *testing.T) {
	t.Setenv("PAYMENT_SANDBOX_SECRET", "")
	// 模拟镜像把 config.example.yaml 当默认配置带上生产的情况。
	cfg, err := loadConfigWith(t, minimalConfig("release", "change-me-payment-secret"))
	if err == nil {
		t.Fatal("release 模式使用了示例占位密钥却通过校验，支付回调验签形同虚设")
	}
	if cfg != nil {
		t.Fatal("校验失败时不应返回配置")
	}
	if !strings.Contains(err.Error(), "payment.sandbox_secret") {
		t.Fatalf("错误信息应指明具体字段，got=%v", err)
	}
}

func TestLoadRejectsEmptyPaymentSecretInReleaseMode(t *testing.T) {
	t.Setenv("PAYMENT_SANDBOX_SECRET", "")
	if _, err := loadConfigWith(t, minimalConfig("release", "")); err == nil {
		t.Fatal("release 模式空支付密钥必须拒绝启动：/payments/sandbox/callback 是公开路由")
	}
}

func TestLoadGeneratesTemporaryPaymentSecretOutsideRelease(t *testing.T) {
	t.Setenv("PAYMENT_SANDBOX_SECRET", "")
	cfg, err := loadConfigWith(t, minimalConfig("debug", ""))
	if err != nil {
		t.Fatalf("本地开发模式不应因缺少支付密钥而启动失败: %v", err)
	}
	if len(cfg.Payment.SandboxSecret) < 16 {
		t.Fatalf("临时密钥长度不足: %d", len(cfg.Payment.SandboxSecret))
	}
}

func TestIsPlaceholderSecret(t *testing.T) {
	placeholders := []string{
		"change-me-please", "changeme", "CHANGE_THIS_SECRET",
		"your-secret-here", "placeholder", "example-secret", "xxx", "",
	}
	for _, value := range placeholders {
		if !isPlaceholderSecret(value) {
			t.Errorf("%q 应被判定为占位值", value)
		}
	}
	reals := []string{
		"9f2c1a4e6b8d0f3a5c7e9b1d3f5a7c9e", "prod-$3cr3t-abcd1234efgh5678",
	}
	for _, value := range reals {
		if isPlaceholderSecret(value) {
			t.Errorf("%q 不应被判定为占位值", value)
		}
	}
}
