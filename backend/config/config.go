package config

import (
	"fmt"
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
	JWT           JWTConfig           `mapstructure:"jwt"`
	TicketQR      TicketQRConfig      `mapstructure:"ticket_qr"`
	Snowflake     SnowflakeConfig     `mapstructure:"snowflake"`
	Log           LogConfig           `mapstructure:"log"`
	RateLimit     RateLimitConfig     `mapstructure:"ratelimit"`
	Cors          CorsConfig          `mapstructure:"cors"`
	OrderConsumer OrderConsumerConfig `mapstructure:"order_consumer"`
	DelayedOrder  OrderDelayConfig    `mapstructure:"delayed_order"`
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
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	PoolSize int    `mapstructure:"pool_size"`
}

type RabbitMQConfig struct {
	URL            string `mapstructure:"url"`
	QueueName      string `mapstructure:"queue_name"`
	RetryQueueName string `mapstructure:"retry_queue_name"`
	DLXName        string `mapstructure:"dlx_name"`
	DLQName        string `mapstructure:"dlq_name"`
}

type JWTConfig struct {
	Secret            string `mapstructure:"secret"`
	ExpireSecs        int    `mapstructure:"expire_secs"`
	RefreshExpireSecs int    `mapstructure:"refresh_expire_secs"`
}

type TicketQRConfig struct {
	Secret           string   `mapstructure:"secret"`
	PreviousSecrets  []string `mapstructure:"previous_secrets"`
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

type RateLimitConfig struct {
	GlobalRate  float64 `mapstructure:"global_rate"`
	GlobalBurst int     `mapstructure:"global_burst"`
	IPRate      float64 `mapstructure:"ip_rate"`
	IPBurst     int     `mapstructure:"ip_burst"`
}

type CorsConfig struct {
	AllowOrigins []string `mapstructure:"allow_origins"`
}

type OrderConsumerConfig struct {
	WorkerCount   int `mapstructure:"worker_count"`
	PrefetchCount int `mapstructure:"prefetch_count"`
	MaxRetries    int `mapstructure:"max_retries"`
}

type OrderDelayConfig struct {
	TimeoutMinutes int `mapstructure:"timeout_minutes"`
	LockTimeoutSec int `mapstructure:"lock_timeout_sec"`
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
		"mysql.conn_max_lifetime",
		"redis.addr",
		"redis.password",
		"redis.db",
		"redis.pool_size",
		"rabbitmq.url",
		"rabbitmq.queue_name",
		"rabbitmq.retry_queue_name",
		"rabbitmq.dlx_name",
		"rabbitmq.dlq_name",
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
		"ratelimit.global_rate",
		"ratelimit.global_burst",
		"ratelimit.ip_rate",
		"ratelimit.ip_burst",
		"order_consumer.worker_count",
		"order_consumer.prefetch_count",
		"order_consumer.max_retries",
		"delayed_order.timeout_minutes",
		"delayed_order.lock_timeout_sec",
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
	v.SetDefault("mysql.conn_max_lifetime", 300)

	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.password", "")
	v.SetDefault("redis.db", 0)
	v.SetDefault("redis.pool_size", 100)

	v.SetDefault("rabbitmq.url", "amqp://guest:guest@localhost:5672/")
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

	v.SetDefault("ratelimit.global_rate", 1000.0)
	v.SetDefault("ratelimit.global_burst", 1200)
	v.SetDefault("ratelimit.ip_rate", 10.0)
	v.SetDefault("ratelimit.ip_burst", 20)

	v.SetDefault("order_consumer.worker_count", 4)
	v.SetDefault("order_consumer.prefetch_count", 5)
	v.SetDefault("order_consumer.max_retries", 3)

	v.SetDefault("delayed_order.timeout_minutes", 15)
	v.SetDefault("delayed_order.lock_timeout_sec", 10)
}

func (c *Config) Validate() error {
	if c.MySQL.DSN == "" {
		return fmt.Errorf("mysql.dsn 不能为空")
	}
	if c.Redis.Addr == "" {
		return fmt.Errorf("redis.addr 不能为空")
	}
	if c.RabbitMQ.URL == "" {
		return fmt.Errorf("rabbitmq.url 不能为空")
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
	if c.OrderConsumer.WorkerCount == 0 {
		c.OrderConsumer.WorkerCount = 4
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
	return nil
}
