package config

import (
	"fmt"

	"github.com/spf13/viper"
)

var GlobalConfig *Config

type Config struct {
	Server        ServerConfig        `mapstructure:"server"`
	MySQL         MySQLConfig         `mapstructure:"mysql"`
	Redis         RedisConfig         `mapstructure:"redis"`
	RabbitMQ      RabbitMQConfig      `mapstructure:"rabbitmq"`
	JWT           JWTConfig           `mapstructure:"jwt"`
	Snowflake     SnowflakeConfig     `mapstructure:"snowflake"`
	Log           LogConfig           `mapstructure:"log"`
	RateLimit     RateLimitConfig     `mapstructure:"ratelimit"`
	Cors          CorsConfig          `mapstructure:"cors"`
	OrderConsumer OrderConsumerConfig `mapstructure:"order_consumer"`
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
	URL       string `mapstructure:"url"`
	QueueName string `mapstructure:"queue_name"`
}

type JWTConfig struct {
	Secret     string `mapstructure:"secret"`
	ExpireSecs int    `mapstructure:"expire_secs"`
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

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("配置校验失败: %w", err)
	}

	GlobalConfig = &cfg
	return &cfg, nil
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
	v.SetDefault("rabbitmq.queue_name", "order_queue")

	v.SetDefault("jwt.expire_secs", 500)

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
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port 必须在 1-65535 之间")
	}
	if c.OrderConsumer.WorkerCount == 0 {
		c.OrderConsumer.WorkerCount = 4
	}
	if c.OrderConsumer.PrefetchCount == 0 {
		c.OrderConsumer.PrefetchCount = 5
	}
	if c.OrderConsumer.WorkerCount < 0 {
		return fmt.Errorf("order_consumer.worker_count 必须大于0")
	}
	if c.OrderConsumer.PrefetchCount < 0 {
		return fmt.Errorf("order_consumer.prefetch_count 必须大于0")
	}
	return nil
}
