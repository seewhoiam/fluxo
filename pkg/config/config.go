package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config represents the complete service configuration
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	Concurrency ConcurrencyConfig `yaml:"concurrency"`
	Performance PerformanceConfig `yaml:"performance"`
	Storage     StorageConfig     `yaml:"storage"`
	OSS         OSSConfig         `yaml:"oss"`
	Security    SecurityConfig    `yaml:"security"`
	Logging     LoggingConfig     `yaml:"logging"`
	Monitoring  MonitoringConfig  `yaml:"monitoring"`
}

// ServerConfig contains gRPC server configuration
type ServerConfig struct {
	Port           int           `yaml:"port"`
	StatusPort     int           `yaml:"status_port"`
	MaxConnections int           `yaml:"max_connections"`
	Timeout        time.Duration `yaml:"timeout"`
}

// ConcurrencyConfig contains task concurrency settings
type ConcurrencyConfig struct {
	MaxConcurrentTasks int           `yaml:"max_concurrent_tasks"`
	TaskQueueSize      int           `yaml:"task_queue_size"`
	QueueTimeout       time.Duration `yaml:"queue_timeout"`
}

// PerformanceConfig contains resource limit settings
type PerformanceConfig struct {
	BufferSize   int64         `yaml:"buffer_size"`
	MaxBatchSize int           `yaml:"max_batch_size"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

// StorageConfig contains temporary file storage settings
type StorageConfig struct {
	TempDirectory  string        `yaml:"temp_directory"`
	TempRetention  time.Duration `yaml:"temp_retention"`
	CleanupEnabled bool          `yaml:"cleanup_enabled"`
}

// OSSConfig contains Alibaba Cloud OSS settings
type OSSConfig struct {
	Endpoint        string        `yaml:"endpoint"`
	Bucket          string        `yaml:"bucket"`
	AccessKeyID     string        `yaml:"access_key_id"`
	AccessKeySecret string        `yaml:"access_key_secret"`
	PartSize        int64         `yaml:"part_size"`
	SignedURLExpiry time.Duration `yaml:"signed_url_expiry"`
	MaxRetries      int           `yaml:"max_retries"`
	ParallelParts   int           `yaml:"parallel_parts"`
	UploadTimeout   time.Duration `yaml:"upload_timeout"`
}

// SecurityConfig contains security settings
type SecurityConfig struct {
	AuthEnabled    bool     `yaml:"auth_enabled"`
	TLSEnabled     bool     `yaml:"tls_enabled"`
	AllowedClients []string `yaml:"allowed_clients"`
}

// LoggingConfig contains logging settings
type LoggingConfig struct {
	Level         string `yaml:"level"`
	Format        string `yaml:"format"`
	Output        string `yaml:"output"`
	EnableTracing bool   `yaml:"enable_tracing"`
}

// MonitoringConfig contains monitoring settings
type MonitoringConfig struct {
	MetricsPort         int           `yaml:"metrics_port"`
	HealthCheckInterval time.Duration `yaml:"health_check_interval"`
}

// DefaultConfig returns a configuration with sensible defaults
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Port:           9090,
			StatusPort:     9091,
			MaxConnections: 100,
			Timeout:        30 * time.Second,
		},
		Concurrency: ConcurrencyConfig{
			MaxConcurrentTasks: 10,
			TaskQueueSize:      100,
			QueueTimeout:       5 * time.Minute,
		},
		Performance: PerformanceConfig{
			BufferSize:   10 * 1024 * 1024, // 10MB
			MaxBatchSize: 1000,
			WriteTimeout: 30 * time.Second,
		},
		Storage: StorageConfig{
			TempDirectory:  "/tmp/export-middleware",
			TempRetention:  1 * time.Hour,
			CleanupEnabled: true,
		},
		OSS: OSSConfig{
			PartSize:        10 * 1024 * 1024, // 10MB
			SignedURLExpiry: 7 * 24 * time.Hour,
			MaxRetries:      3,
			ParallelParts:   5,
			UploadTimeout:   30 * time.Minute,
		},
		Security: SecurityConfig{
			AuthEnabled:    false,
			TLSEnabled:     false,
			AllowedClients: []string{},
		},
		Logging: LoggingConfig{
			Level:         "info",
			Format:        "json",
			Output:        "stdout",
			EnableTracing: false,
		},
		Monitoring: MonitoringConfig{
			MetricsPort:         8080,
			HealthCheckInterval: 30 * time.Second,
		},
	}
}

// LoadConfig loads configuration from a YAML file
func LoadConfig(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// If config file doesn't exist, return default config
			return cfg, nil
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Override with environment variables if set
	cfg.applyEnvOverrides()

	// Validate configuration
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return cfg, nil
}

// applyEnvOverrides applies environment variable overrides
func (c *Config) applyEnvOverrides() {
	if val := os.Getenv("OSS_ENDPOINT"); val != "" {
		c.OSS.Endpoint = val
	}
	if val := os.Getenv("OSS_BUCKET"); val != "" {
		c.OSS.Bucket = val
	}
	if val := os.Getenv("OSS_ACCESS_KEY_ID"); val != "" {
		c.OSS.AccessKeyID = val
	}
	if val := os.Getenv("OSS_ACCESS_KEY_SECRET"); val != "" {
		c.OSS.AccessKeySecret = val
	}
	if val := os.Getenv("LOG_LEVEL"); val != "" {
		c.Logging.Level = val
	}
}

// Validate checks if the configuration is valid
func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", c.Server.Port)
	}
	if c.Server.StatusPort <= 0 || c.Server.StatusPort > 65535 {
		return fmt.Errorf("invalid status port: %d", c.Server.StatusPort)
	}
	if c.Concurrency.MaxConcurrentTasks <= 0 {
		return fmt.Errorf("max concurrent tasks must be positive")
	}
	if c.Concurrency.TaskQueueSize < 0 {
		return fmt.Errorf("task queue size cannot be negative")
	}
	if c.OSS.Endpoint == "" {
		return fmt.Errorf("OSS endpoint is required")
	}
	if c.OSS.Bucket == "" {
		return fmt.Errorf("OSS bucket is required")
	}
	if c.OSS.AccessKeyID == "" {
		return fmt.Errorf("OSS access key ID is required")
	}
	if c.OSS.AccessKeySecret == "" {
		return fmt.Errorf("OSS access key secret is required")
	}
	return nil
}
