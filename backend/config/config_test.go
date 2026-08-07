package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_ValidYAML(t *testing.T) {
	t.Setenv("INVENTORY_BUCKETS_ENABLED", "")
	t.Setenv("INVENTORY_BUCKET_COUNT", "")
	t.Setenv("INVENTORY_MIN_QUOTA_TO_BUCKET", "")
	t.Setenv("INVENTORY_BUCKET_RETRY", "")
	dir := t.TempDir()
	content := `
server:
  host: "0.0.0.0"
  port: 9090
  mode: "release"
mysql:
  dsn: "test:test@tcp(localhost:3306)/testdb?charset=utf8mb4&parseTime=True"
  max_idle_conns: 5
  max_open_conns: 50
  conn_max_lifetime: 600
redis:
  addr: "redis.example.com:6380"
  password: "pwd123"
  db: 1
  pool_size: 50
rabbitmq:
  url: "amqp://user:pass@localhost:5672/"
  queue_name: "test_queue"
jwt:
  secret: "test-secret"
  expire_secs: 3600
ticket_qr:
  secret: "test-ticket-qr-secret"
snowflake:
  node_id: 2
log:
  level: "info"
  file_path: "/var/log/app.log"
  max_size: 200
  max_backups: 14
  max_age: 60
ratelimit:
  global_rate: 500
  global_burst: 600
  ip_rate: 5
  ip_burst: 10
cors:
  allow_origins:
    - "http://localhost:3000"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Server.Mode != "release" {
		t.Errorf("expected mode release, got %s", cfg.Server.Mode)
	}
	if cfg.MySQL.DSN != "test:test@tcp(localhost:3306)/testdb?charset=utf8mb4&parseTime=True" {
		t.Errorf("unexpected MySQL DSN: %s", cfg.MySQL.DSN)
	}
	if cfg.Redis.DB != 1 {
		t.Errorf("expected Redis DB 1, got %d", cfg.Redis.DB)
	}
	if cfg.JWT.ExpireSecs != 3600 {
		t.Errorf("expected JWT expire 3600, got %d", cfg.JWT.ExpireSecs)
	}
	if cfg.Snowflake.NodeID != 2 {
		t.Errorf("expected NodeID 2, got %d", cfg.Snowflake.NodeID)
	}
	if len(cfg.Cors.AllowOrigins) != 1 {
		t.Errorf("expected 1 allow origin, got %d", len(cfg.Cors.AllowOrigins))
	}
	if cfg.Pprof.Enabled || cfg.Pprof.Host != "127.0.0.1" || cfg.Pprof.Port != 6060 {
		t.Errorf("unexpected pprof defaults: %#v", cfg.Pprof)
	}
	if !cfg.RateLimit.DistributedWriteEnabled || cfg.RateLimit.WriteMaxPerWindow != 10 {
		t.Errorf("unexpected distributed rate limit defaults: %#v", cfg.RateLimit)
	}
	if cfg.Inventory.BucketsEnabled || cfg.Inventory.BucketCount != 8 ||
		cfg.Inventory.MinQuotaToBucket != 64 || cfg.Inventory.BucketRetry != 4 {
		t.Errorf("unexpected inventory defaults: %#v", cfg.Inventory)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	content := `server: [invalid yaml {{{`
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(content), 0644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestValidate_EmptyDSN(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		MySQL:    MySQLConfig{DSN: ""},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: "test"},
		TicketQR: TicketQRConfig{Secret: "test-ticket-qr-secret"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty DSN")
	}
}

func TestValidate_EmptyRedisAddr(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		MySQL:    MySQLConfig{DSN: "test"},
		Redis:    RedisConfig{Addr: ""},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: "test"},
		TicketQR: TicketQRConfig{Secret: "test-ticket-qr-secret"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty Redis addr")
	}
}

func TestValidate_EmptyJWTSecret(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		MySQL:    MySQLConfig{DSN: "test"},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: ""},
		TicketQR: TicketQRConfig{Secret: "test-ticket-qr-secret"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for empty JWT secret")
	}
}

func TestValidate_ShortTicketQRSecret(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		MySQL:    MySQLConfig{DSN: "test"},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: "test"},
		TicketQR: TicketQRConfig{Secret: "too-short"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for short ticket QR secret")
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 0},
		MySQL:    MySQLConfig{DSN: "test"},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: "test"},
		TicketQR: TicketQRConfig{Secret: "test-ticket-qr-secret"},
	}
	if err := cfg.Validate(); err == nil {
		t.Error("expected error for invalid port")
	}
}

func TestValidate_RejectsPublicPprofAddress(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		MySQL:    MySQLConfig{DSN: "test"},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: "test"},
		TicketQR: TicketQRConfig{Secret: "test-ticket-qr-secret"},
		Pprof:    PprofConfig{Enabled: true, Host: "0.0.0.0", Port: 6060},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected public pprof address to be rejected")
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := &Config{
		Server:   ServerConfig{Port: 8080},
		MySQL:    MySQLConfig{DSN: "test-dsn"},
		Redis:    RedisConfig{Addr: "localhost:6379"},
		RabbitMQ: RabbitMQConfig{URL: "amqp://localhost"},
		JWT:      JWTConfig{Secret: "test"},
		TicketQR: TicketQRConfig{Secret: "test-ticket-qr-secret"},
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestElasticsearchPreferModes(t *testing.T) {
	cases := []struct {
		enabled bool
		engine  string
		want    bool
	}{
		{false, "auto", false},
		{true, "mysql", false},
		{true, "auto", true},
		{true, "elasticsearch", true},
	}
	for _, tc := range cases {
		cfg := ElasticsearchConfig{Enabled: tc.enabled, SearchEngine: tc.engine}
		if got := cfg.PreferElasticsearch(); got != tc.want {
			t.Fatalf("enabled=%v engine=%s prefer=%v want=%v", tc.enabled, tc.engine, got, tc.want)
		}
	}
}

func TestLoad_DefaultsApplied(t *testing.T) {
	dir := t.TempDir()
	content := `
mysql:
  dsn: "minimal:dsn@tcp(localhost)/db"
redis:
  addr: "localhost:6379"
rabbitmq:
  url: "amqp://localhost"
jwt:
  secret: "test"
ticket_qr:
  secret: "test-ticket-qr-secret"
`
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check defaults are applied for unspecified fields
	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.Mode != "debug" {
		t.Errorf("expected default mode debug, got %s", cfg.Server.Mode)
	}
	if cfg.Redis.DB != 0 {
		t.Errorf("expected default Redis DB 0, got %d", cfg.Redis.DB)
	}
	if cfg.Snowflake.NodeID != 1 {
		t.Errorf("expected default NodeID 1, got %d", cfg.Snowflake.NodeID)
	}
}

func TestLoad_EnvironmentOverrides(t *testing.T) {
	t.Setenv("SERVER_HOST", "0.0.0.0")
	t.Setenv("MYSQL_DSN", "env:dsn@tcp(mysql)/db")
	t.Setenv("JWT_SECRET", "env-secret")
	t.Setenv("CORS_ALLOW_ORIGINS", "https://example.com, http://localhost")

	dir := t.TempDir()
	content := `
mysql:
  dsn: "file:dsn@tcp(localhost)/db"
redis:
  addr: "localhost:6379"
rabbitmq:
  url: "amqp://localhost"
jwt:
  secret: "file-secret"
ticket_qr:
  secret: "test-ticket-qr-secret"
`
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Server.Host != "0.0.0.0" {
		t.Errorf("expected env server host, got %s", cfg.Server.Host)
	}
	if cfg.MySQL.DSN != "env:dsn@tcp(mysql)/db" {
		t.Errorf("expected env MySQL DSN, got %s", cfg.MySQL.DSN)
	}
	if cfg.JWT.Secret != "env-secret" {
		t.Errorf("expected env JWT secret, got %s", cfg.JWT.Secret)
	}
	if len(cfg.Cors.AllowOrigins) != 2 || cfg.Cors.AllowOrigins[0] != "https://example.com" {
		t.Errorf("unexpected CORS origins: %#v", cfg.Cors.AllowOrigins)
	}
}

func TestLoad_RedisSentinelEnvironment(t *testing.T) {
	t.Setenv("REDIS_MASTER_NAME", "mymaster")
	t.Setenv("REDIS_SENTINEL_ADDRS", "sentinel-1:26379, sentinel-2:26379")

	dir := t.TempDir()
	content := `
mysql:
  dsn: "test:test@tcp(mysql)/db"
redis:
  addr: ""
rabbitmq:
  url: "amqp://localhost"
jwt:
  secret: "test"
ticket_qr:
  secret: "test-ticket-qr-secret"
`
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Redis.MasterName != "mymaster" {
		t.Fatalf("unexpected master name: %s", cfg.Redis.MasterName)
	}
	if len(cfg.Redis.SentinelAddrs) != 2 {
		t.Fatalf("unexpected sentinel addresses: %#v", cfg.Redis.SentinelAddrs)
	}
}
