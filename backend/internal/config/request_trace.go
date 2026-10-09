package config

import "fmt"

// RequestTraceConfig controls the separate, unsampled gateway forensic log.
// Bodies contain user content; credentials in headers and URLs are redacted.
type RequestTraceConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	Directory    string `mapstructure:"directory"`
	MaxSizeMB    int    `mapstructure:"max_size_mb"`
	MaxBackups   int    `mapstructure:"max_backups"`
	MaxAgeDays   int    `mapstructure:"max_age_days"`
	MaxBodyBytes int64  `mapstructure:"max_body_bytes"` // 0 captures all bytes consumed/written.
}

func (c RequestTraceConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.MaxSizeMB < 1 || c.MaxSizeMB > 1024 || c.MaxBackups < 1 || c.MaxBackups > 10000 || c.MaxAgeDays < 1 || c.MaxBodyBytes < 0 {
		return fmt.Errorf("gateway.request_trace requires max_size_mb 1..1024, max_backups 1..10000, max_age_days >= 1 and max_body_bytes >= 0")
	}
	return nil
}
