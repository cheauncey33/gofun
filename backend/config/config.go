package config

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/spf13/viper"
)

var GlobalConfig *Config

type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	MySQL         MySQLConfig         `mapstructure:"mysql"`
	Redis         RedisConfig         `mapstructure:"redis"`
	RabbitMQ      RabbitMQConfig      `mapstructure:"rabbitmq"`
	Elasticsearch ElasticsearchConfig `mapstructure:"elasticsearch"`
	JWT           JWTConfig           `mapstructure:"jwt"`
	TicketQR      TicketQRConfig      `mapstructure:"ticket_qr"`
	Snowflake     SnowflakeConfig     `mapstructure:"snowflake"`
	Log           LogConfig           `mapstructure:"log"`
	Pprof         PprofConfig         `mapstructure:"pprof"`
	Telemetry     TelemetryConfig     `mapstructure:"telemetry"`
	RateLimit     RateLimitConfig     `mapstructure:"ratelimit"`
	Cors          CorsConfig          `mapstructure:"cors"`
	OrderConsumer OrderConsumerConfig `mapstructure:"order_consumer"`
	OrderOutbox   OrderOutboxConfig   `mapstructure:"order_outbox"`
	DelayedOrder  OrderDelayConfig    `mapstructure:"delayed_order"`
	RushSale      RushSaleConfig      `mapstructure:"rush_sale"`
	Inventory     InventoryConfig     `mapstructure:"inventory"`
	Payment       PaymentConfig       `mapstructure:"payment"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

type MySQLConfig struct {
	DSN             string `mapstructure:"dsn"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	// WorkerMaxOpenConns > 0 时为 Consumer/Outbox/Timeout 等后台路径单独开池（bulkhead）。
	// 0 表示与 HTTP 共用 MaxOpenConns，保持历史行为。
	WorkerMaxOpenConns int `mapstructure:"worker_max_open_conns"`
	WorkerMaxIdleConns int `mapstructure:"worker_max_idle_conns"`
	ConnMaxLifetime     int `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	Addr          string   `mapstructure:"addr"`
	Password      string   `mapstructure:"password"`
	DB            int      `mapstructure:"db"`
	PoolSize      int      `mapstructure:"pool_size"`
	MasterName    string   `mapstructure:"master_name"`
	SentinelAddrs []string `mapstructure:"sentinel_addrs"`
}

type RabbitMQConfig struct {
	URL            string `mapstructure:"url"`
	QueueType      string `mapstructure:"queue_type"`
	QueueName      string `mapstructure:"queue_name"`
	RetryQueueName string `mapstructure:"retry_queue_name"`
	DLXName        string `mapstructure:"dlx_name"`
	DLQName        string `mapstructure:"dlq_name"`
}

// ElasticsearchConfig 活动目录全文检索（可选）。
// enabled=false 时完全走 MySQL LIKE；enabled=true 且 search_engine 为 auto/elasticsearch 时关键词走 ES，失败降级 LIKE。
type ElasticsearchConfig struct {
	Enabled             bool     `mapstructure:"enabled"`
	Addresses           []string `mapstructure:"addresses"`
	Username            string   `mapstructure:"username"`
	Password            string   `mapstructure:"password"`
	Index               string   `mapstructure:"index"`
	SearchEngine        string   `mapstructure:"search_engine"` // mysql | elasticsearch | auto
	SyncIntervalMinutes int      `mapstructure:"sync_interval_minutes"`
	SyncLockTimeoutSec  int      `mapstructure:"sync_lock_timeout_sec"`
}

type JWTConfig struct {
	Secret            string `mapstructure:"secret"`
	ExpireSecs        int    `mapstructure:"expire_secs"`
	RefreshExpireSecs int    `mapstructure:"refresh_expire_secs"`
}

type TicketQRConfig struct {
	Secret          string   `mapstructure:"secret"`
	PreviousSecrets []string `mapstructure:"previous_secrets"`
}

type SnowflakeConfig struct {
	NodeID int64 `mapstructure:"node_id"`
}

type LogConfig struct {
	Level      string `mapstructure:"level"`
	FilePath   string `mapstructure:"file_path"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
}

type PprofConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Host    string `mapstructure:"host"`
	Port    int    `mapstructure:"port"`
}

type TelemetryConfig struct {
	Enabled      bool    `mapstructure:"enabled"`
	ServiceName  string  `mapstructure:"service_name"`
	OTLPEndpoint string  `mapstructure:"otlp_endpoint"`
	Insecure     bool    `mapstructure:"insecure"`
	SampleRatio  float64 `mapstructure:"sample_ratio"`
}

type RateLimitConfig struct {
	GlobalRate              float64 `mapstructure:"global_rate"`
	GlobalBurst             int     `mapstructure:"global_burst"`
	IPRate                  float64 `mapstructure:"ip_rate"`
	IPBurst                 int     `mapstructure:"ip_burst"`
	WriteRate               float64 `mapstructure:"write_rate"`
	WriteBurst              int     `mapstructure:"write_burst"`
	DistributedWriteEnabled bool    `mapstructure:"distributed_write_enabled"`
	WriteWindowMS           int     `mapstructure:"write_window_ms"`
	WriteMaxPerWindow       int64   `mapstructure:"write_max_per_window"`
	WriteFailOpen           bool    `mapstructure:"write_fail_open"`
}

type CorsConfig struct {
	AllowOrigins []string `mapstructure:"allow_origins"`
}

type OrderConsumerConfig struct {
	WorkerCount   int `mapstructure:"worker_count"`
	PrefetchCount int `mapstructure:"prefetch_count"`
	MaxRetries    int `mapstructure:"max_retries"`
}

// OrderOutboxConfig 控制 outbox 后台投递。
type OrderOutboxConfig struct {
	PublishWorkers int `mapstructure:"publish_workers"`
	PublishBatch   int `mapstructure:"publish_batch"`
	TickIntervalMS int `mapstructure:"tick_interval_ms"`
}

type OrderDelayConfig struct {
	TimeoutMinutes int `mapstructure:"timeout_minutes"`
	LockTimeoutSec int `mapstructure:"lock_timeout_sec"`
	WorkerCount    int `mapstructure:"worker_count"`
}

type RushSaleConfig struct {
	CampaignCacheTTLMS int `mapstructure:"campaign_cache_ttl_ms"`
}

type PaymentConfig struct {
	Provider               string `mapstructure:"provider"`
	SandboxSecret          string `mapstructure:"sandbox_secret"`
	SandboxCallbackDelayMS int    `mapstructure:"sandbox_callback_delay_ms"`
}

// InventoryConfig 票档/抢票 Redis+MySQL 同构分桶。
// 生产基线固定使用分桶；关闭仅用于兼容性测试或迁移场景。
type InventoryConfig struct {
	BucketsEnabled   bool `mapstructure:"buckets_enabled"`
	BucketCount      int  `mapstructure:"bucket_count"`
	MinQuotaToBucket int  `mapstructure:"min_quota_to_bucket"`
	BucketRetry      int  `mapstructure:"bucket_retry"`
}

func Load(configPath string) (*Config, error) {
	v := viper.New()
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./config")
		v.AddConfigPath(".")
	}

	setDefaults(v)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	bindEnvs(v,
		"server.host",
		"server.port",
		"server.mode",
		"mysql.dsn",
		"mysql.max_idle_conns",
		"mysql.max_open_conns",
		"mysql.worker_max_open_conns",
		"mysql.worker_max_idle_conns",
		"mysql.conn_max_lifetime",
		"redis.addr",
		"redis.password",
		"redis.db",
		"redis.pool_size",
		"redis.master_name",
		"rabbitmq.url",
		"rabbitmq.queue_type",
		"rabbitmq.queue_name",
		"rabbitmq.retry_queue_name",
		"rabbitmq.dlx_name",
		"rabbitmq.dlq_name",
		"elasticsearch.enabled",
		"elasticsearch.username",
		"elasticsearch.password",
		"elasticsearch.index",
		"elasticsearch.search_engine",
		"elasticsearch.sync_interval_minutes",
		"elasticsearch.sync_lock_timeout_sec",
		"jwt.secret",
		"jwt.expire_secs",
		"jwt.refresh_expire_secs",
		"ticket_qr.secret",
		"snowflake.node_id",
		"log.level",
		"log.file_path",
		"log.max_size",
		"log.max_backups",
		"log.max_age",
		"pprof.enabled",
		"pprof.host",
		"pprof.port",
		"telemetry.enabled",
		"telemetry.service_name",
		"telemetry.otlp_endpoint",
		"telemetry.insecure",
		"telemetry.sample_ratio",
		"ratelimit.global_rate",
		"ratelimit.global_burst",
		"ratelimit.ip_rate",
		"ratelimit.ip_burst",
		"ratelimit.write_rate",
		"ratelimit.write_burst",
		"ratelimit.distributed_write_enabled",
		"ratelimit.write_window_ms",
		"ratelimit.write_max_per_window",
		"ratelimit.write_fail_open",
		"order_consumer.worker_count",
		"order_consumer.prefetch_count",
		"order_consumer.max_retries",
		"order_outbox.publish_workers",
		"order_outbox.publish_batch",
		"order_outbox.tick_interval_ms",
		"delayed_order.timeout_minutes",
		"delayed_order.lock_timeout_sec",
		"delayed_order.worker_count",
		"rush_sale.campaign_cache_ttl_ms",
		"inventory.buckets_enabled",
		"inventory.bucket_count",
		"inventory.min_quota_to_bucket",
		"inventory.bucket_retry",
		"payment.provider",
		"payment.sandbox_secret",
		"payment.sandbox_callback_delay_ms",
	)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}
	if origins := os.Getenv("CORS_ALLOW_ORIGINS"); origins != "" {
		cfg.Cors.AllowOrigins = splitCSV(origins)
	}
	if addrs := os.Getenv("ELASTICSEARCH_ADDRESSES"); addrs != "" {
		cfg.Elasticsearch.Addresses = splitCSV(addrs)
	}
	if addrs := os.Getenv("REDIS_SENTINEL_ADDRS"); addrs != "" {
		cfg.Redis.SentinelAddrs = splitCSV(addrs)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}

	GlobalConfig = &cfg
	return &cfg, nil
}

func bindEnvs(v *viper.Viper, keys ...string) {
	for _, key := range keys {
		_ = v.BindEnv(key)
	}
}

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			values = append(values, part)
		}
	}
	return values
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("server.host", "127.0.0.1")
	v.SetDefault("server.port", 8080)
	v.SetDefault("server.mode", "debug")

	v.SetDefault("mysql.max_idle_conns", 10)
	v.SetDefault("mysql.max_open_conns", 100)
	v.SetDefault("mysql.worker_max_open_conns", 0)
	v.SetDefault("mysql.worker_max_idle_conns", 10)
	v.SetDefault("mysql.conn_max_lifetime", 300)

	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.pool_size", 100)
	v.SetDefault("redis.master_name", "")
	v.SetDefault("redis.sentinel_addrs", []string{})

	v.SetDefault("rabbitmq.url", "amqp://guest:guest@localhost:5672/")
	v.SetDefault("rabbitmq.queue_type", "classic")
	v.SetDefault("rabbitmq.queue_name", "fuchang.order.queue")
	v.SetDefault("rabbitmq.retry_queue_name", "fuchang.order.retry")
	v.SetDefault("rabbitmq.dlx_name", "fuchang.order.dlx")
	v.SetDefault("rabbitmq.dlq_name", "fuchang.order.dead")

	v.SetDefault("jwt.expire_secs", 500)
	v.SetDefault("jwt.refresh_expire_secs", 7*24*60*60)

	v.SetDefault("snowflake.node_id", 1)

	v.SetDefault("log.level", "debug")
	v.SetDefault("log.file_path", "")
	v.SetDefault("log.max_size", 100)
	v.SetDefault("log.max_backups", 7)
	v.SetDefault("log.max_age", 30)

	v.SetDefault("pprof.enabled", false)
	v.SetDefault("pprof.host", "127.0.0.1")
	v.SetDefault("pprof.port", 6060)

	v.SetDefault("telemetry.enabled", false)
	v.SetDefault("telemetry.service_name", "gofun-ticketing")
	v.SetDefault("telemetry.otlp_endpoint", "127.0.0.1:4317")
	v.SetDefault("telemetry.insecure", true)
	v.SetDefault("telemetry.sample_ratio", 0.05)

	v.SetDefault("ratelimit.global_rate", 1000.0)
	v.SetDefault("ratelimit.global_burst", 1200)
	v.SetDefault("ratelimit.ip_rate", 10.0)
	v.SetDefault("ratelimit.ip_burst", 20)
	v.SetDefault("ratelimit.write_rate", 5.0)
	v.SetDefault("ratelimit.write_burst", 10)
	v.SetDefault("ratelimit.distributed_write_enabled", true)
	v.SetDefault("ratelimit.write_window_ms", 1000)
	v.SetDefault("ratelimit.write_max_per_window", 10)
	v.SetDefault("ratelimit.write_fail_open", false)

	v.SetDefault("order_consumer.worker_count", 6)
	v.SetDefault("order_consumer.prefetch_count", 5)
	v.SetDefault("order_consumer.max_retries", 3)
	v.SetDefault("order_outbox.publish_workers", 4)
	v.SetDefault("order_outbox.publish_batch", 200)
	v.SetDefault("order_outbox.tick_interval_ms", 200)

	v.SetDefault("delayed_order.timeout_minutes", 15)
	v.SetDefault("delayed_order.lock_timeout_sec", 10)
	v.SetDefault("delayed_order.worker_count", 2)
	v.SetDefault("rush_sale.campaign_cache_ttl_ms", 3000)

	v.SetDefault("inventory.buckets_enabled", true)
	v.SetDefault("inventory.bucket_count", 32)
	v.SetDefault("inventory.min_quota_to_bucket", 64)
	v.SetDefault("inventory.bucket_retry", 4)

	v.SetDefault("payment.provider", "sandbox")
	v.SetDefault("payment.sandbox_callback_delay_ms", 500)

	v.SetDefault("elasticsearch.enabled", false)
	v.SetDefault("elasticsearch.index", "fuchang_events")
	v.SetDefault("elasticsearch.search_engine", "auto")
	v.SetDefault("elasticsearch.addresses", []string{"http://127.0.0.1:9200"})
	v.SetDefault("elasticsearch.sync_interval_minutes", 15)
	v.SetDefault("elasticsearch.sync_lock_timeout_sec", 300)
}

func (c *Config) Validate() error {
	if c.MySQL.DSN == "" {
		return fmt.Errorf("mysql.dsn 不能为空")
	}
	if strings.TrimSpace(c.Redis.MasterName) == "" && strings.TrimSpace(c.Redis.Addr) == "" {
		return fmt.Errorf("redis.addr 不能为空")
	}
	if strings.TrimSpace(c.Redis.MasterName) != "" && len(c.Redis.SentinelAddrs) == 0 {
		return fmt.Errorf("redis.sentinel_addrs 不能为空")
	}
	if c.RabbitMQ.URL == "" {
		return fmt.Errorf("rabbitmq.url 不能为空")
	}
	c.RabbitMQ.QueueType = strings.ToLower(strings.TrimSpace(c.RabbitMQ.QueueType))
	if c.RabbitMQ.QueueType == "" {
		c.RabbitMQ.QueueType = "classic"
	}
	if c.RabbitMQ.QueueType != "classic" && c.RabbitMQ.QueueType != "quorum" {
		return fmt.Errorf("rabbitmq.queue_type 必须是 classic|quorum")
	}
	if c.JWT.Secret == "" {
		return fmt.Errorf("jwt.secret 不能为空")
	}
	if len(c.TicketQR.Secret) < 16 {
		return fmt.Errorf("ticket_qr.secret 至少需要 16 个字符")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 必须在 1-65535 之间")
	}
	if c.Pprof.Enabled {
		ip := net.ParseIP(c.Pprof.Host)
		if c.Pprof.Host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return fmt.Errorf("pprof.host 必须是回环地址")
		}
		if c.Pprof.Port <= 0 || c.Pprof.Port > 65535 {
			return fmt.Errorf("pprof.port 必须在 1-65535 之间")
		}
	}
	if c.Telemetry.Enabled {
		if strings.TrimSpace(c.Telemetry.ServiceName) == "" {
			return fmt.Errorf("telemetry.service_name 不能为空")
		}
		if strings.TrimSpace(c.Telemetry.OTLPEndpoint) == "" {
			return fmt.Errorf("telemetry.otlp_endpoint 不能为空")
		}
		if c.Telemetry.SampleRatio < 0 || c.Telemetry.SampleRatio > 1 {
			return fmt.Errorf("telemetry.sample_ratio 必须在0到1之间")
		}
	}
	if c.RateLimit.GlobalRate == 0 && c.RateLimit.GlobalBurst == 0 {
		c.RateLimit.GlobalRate, c.RateLimit.GlobalBurst = 1000, 1200
	}
	if c.RateLimit.IPRate == 0 && c.RateLimit.IPBurst == 0 {
		c.RateLimit.IPRate, c.RateLimit.IPBurst = 10, 20
	}
	if c.RateLimit.WriteRate == 0 && c.RateLimit.WriteBurst == 0 {
		c.RateLimit.WriteRate, c.RateLimit.WriteBurst = 5, 10
	}
	if c.RateLimit.GlobalRate <= 0 || c.RateLimit.GlobalBurst <= 0 ||
		c.RateLimit.IPRate <= 0 || c.RateLimit.IPBurst <= 0 ||
		c.RateLimit.WriteRate <= 0 || c.RateLimit.WriteBurst <= 0 {
		return fmt.Errorf("ratelimit 速率和 burst 必须大于0")
	}
	if c.RateLimit.DistributedWriteEnabled &&
		(c.RateLimit.WriteWindowMS <= 0 || c.RateLimit.WriteMaxPerWindow <= 0) {
		return fmt.Errorf("分布式写限流窗口和配额必须大于0")
	}
	if c.RushSale.CampaignCacheTTLMS < 0 {
		return fmt.Errorf("rush_sale.campaign_cache_ttl_ms 必须大于等于0")
	}
	if c.Inventory.BucketCount == 0 {
		c.Inventory.BucketCount = 32
	}
	if c.Inventory.MinQuotaToBucket == 0 {
		c.Inventory.MinQuotaToBucket = 64
	}
	if c.Inventory.BucketCount < 1 {
		return fmt.Errorf("inventory.bucket_count 必须大于等于1")
	}
	if c.Inventory.MinQuotaToBucket < 1 {
		return fmt.Errorf("inventory.min_quota_to_bucket 必须大于等于1")
	}
	if c.Inventory.BucketRetry < 0 {
		return fmt.Errorf("inventory.bucket_retry 必须大于等于0")
	}
	c.Payment.Provider = strings.ToLower(strings.TrimSpace(c.Payment.Provider))
	if c.Payment.Provider == "" {
		c.Payment.Provider = "sandbox"
	}
	if c.Payment.Provider != "sandbox" {
		return fmt.Errorf("payment.provider 当前仅支持 sandbox")
	}
	if c.Payment.SandboxSecret != "" && len(c.Payment.SandboxSecret) < 16 {
		return fmt.Errorf("payment.sandbox_secret 至少需要 16 个字符")
	}
	if c.Payment.SandboxCallbackDelayMS <= 0 {
		c.Payment.SandboxCallbackDelayMS = 500
	}
	if c.Payment.SandboxCallbackDelayMS > 60000 {
		return fmt.Errorf("payment.sandbox_callback_delay_ms 不能超过 60000")
	}
	if c.OrderConsumer.WorkerCount == 0 {
		c.OrderConsumer.WorkerCount = 6
	}
	if c.OrderConsumer.PrefetchCount == 0 {
		c.OrderConsumer.PrefetchCount = 5
	}
	if c.OrderConsumer.MaxRetries == 0 {
		c.OrderConsumer.MaxRetries = 3
	}
	if c.OrderConsumer.WorkerCount < 0 {
		return fmt.Errorf("order_consumer.worker_count 必须大于0")
	}
	if c.OrderConsumer.PrefetchCount < 0 {
		return fmt.Errorf("order_consumer.prefetch_count 必须大于0")
	}
	if c.OrderConsumer.MaxRetries < 0 {
		return fmt.Errorf("order_consumer.max_retries 必须大于等于0")
	}
	if c.DelayedOrder.WorkerCount == 0 {
		c.DelayedOrder.WorkerCount = 2
	}
	if c.DelayedOrder.WorkerCount < 0 {
		return fmt.Errorf("delayed_order.worker_count 必须大于0")
	}
	if c.OrderOutbox.PublishWorkers == 0 {
		c.OrderOutbox.PublishWorkers = 4
	}
	if c.OrderOutbox.PublishBatch == 0 {
		c.OrderOutbox.PublishBatch = 200
	}
	if c.OrderOutbox.TickIntervalMS == 0 {
		c.OrderOutbox.TickIntervalMS = 200
	}
	if c.OrderOutbox.PublishWorkers < 0 {
		return fmt.Errorf("order_outbox.publish_workers 必须大于0")
	}
	if c.OrderOutbox.PublishBatch < 0 {
		return fmt.Errorf("order_outbox.publish_batch 必须大于0")
	}
	if c.OrderOutbox.TickIntervalMS < 0 {
		return fmt.Errorf("order_outbox.tick_interval_ms 必须大于0")
	}
	engine := strings.ToLower(strings.TrimSpace(c.Elasticsearch.SearchEngine))
	if engine == "" {
		engine = "auto"
		c.Elasticsearch.SearchEngine = engine
	}
	switch engine {
	case "mysql", "elasticsearch", "auto":
	default:
		return fmt.Errorf("elasticsearch.search_engine 必须是 mysql|elasticsearch|auto")
	}
	if c.Elasticsearch.Index == "" {
		c.Elasticsearch.Index = "fuchang_events"
	}
	if c.Elasticsearch.SyncIntervalMinutes < 0 {
		return fmt.Errorf("elasticsearch.sync_interval_minutes 必须大于等于0")
	}
	if c.Elasticsearch.SyncLockTimeoutSec <= 0 {
		c.Elasticsearch.SyncLockTimeoutSec = 300
	}
	return nil
}

// PreferElasticsearch 是否对关键词检索优先走 ES（失败可由调用方降级）。
func (c ElasticsearchConfig) PreferElasticsearch() bool {
	if !c.Enabled {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(c.SearchEngine)) {
	case "elasticsearch", "auto":
		return true
	default:
		return false
	}
}
