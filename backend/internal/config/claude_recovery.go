package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
)

// ClaudeRecoveryConfig is opt-in. Summary credentials belong to the operator;
// they are never accepted from a gateway request or stored in checkpoints.
type ClaudeRecoveryConfig struct {
	MaxSummaryInputBytes      int     `mapstructure:"max_summary_input_bytes"`
	CheckpointEveryTurns      int     `mapstructure:"checkpoint_every_turns"`
	ContextWindowTokens       int     `mapstructure:"context_window_tokens"`
	Enabled                   bool    `mapstructure:"enabled"`
	GroupIDs                  []int64 `mapstructure:"group_ids"`
	EncryptionKey             string  `mapstructure:"encryption_key" json:"-"`
	SummaryURL                string  `mapstructure:"summary_url"`
	SummaryAPIKey             string  `mapstructure:"summary_api_key" json:"-"`
	SummaryModel              string  `mapstructure:"summary_model"`
	MaxHistoryBytes           int     `mapstructure:"max_history_bytes"`
	MaxSummaryTokens          int     `mapstructure:"max_summary_tokens"`
	MaxSummaryCallsPerUserDay int     `mapstructure:"max_summary_calls_per_user_day"`
	RetentionHours            int     `mapstructure:"retention_hours"`
	LeaseSeconds              int     `mapstructure:"lease_seconds"`
	SummaryTimeoutSeconds     int     `mapstructure:"summary_timeout_seconds"`
}

func (c ClaudeRecoveryConfig) WithDefaults() ClaudeRecoveryConfig {
	if c.MaxSummaryInputBytes == 0 {
		c.MaxSummaryInputBytes = 256 * 1024
	}
	if c.CheckpointEveryTurns == 0 {
		c.CheckpointEveryTurns = 3
	}
	if c.ContextWindowTokens == 0 {
		c.ContextWindowTokens = 200000
	}
	if c.SummaryURL == "" {
		c.SummaryURL = "https://api.anthropic.com/v1/messages"
	}
	if c.MaxHistoryBytes == 0 {
		c.MaxHistoryBytes = 8 * 1024 * 1024
	}
	if c.MaxSummaryTokens == 0 {
		c.MaxSummaryTokens = 4096
	}
	if c.MaxSummaryCallsPerUserDay == 0 {
		c.MaxSummaryCallsPerUserDay = 20
	}
	if c.RetentionHours == 0 {
		c.RetentionHours = 168
	}
	if c.LeaseSeconds == 0 {
		c.LeaseSeconds = 120
	}
	if c.SummaryTimeoutSeconds == 0 {
		c.SummaryTimeoutSeconds = 45
	}
	return c
}

func (c ClaudeRecoveryConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	c = c.WithDefaults()
	key, err := base64.StdEncoding.DecodeString(c.EncryptionKey)
	if err != nil || len(key) != 32 {
		return fmt.Errorf("gateway.claude_recovery.encryption_key must be a base64 encoded 32-byte key")
	}
	u, err := url.Parse(c.SummaryURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("gateway.claude_recovery.summary_url must be an HTTPS endpoint without userinfo or fragment")
	}
	if len(c.GroupIDs) == 0 || c.SummaryAPIKey == "" || c.SummaryModel == "" {
		return fmt.Errorf("managed Claude recovery requires explicit group_ids and summary credentials/model")
	}
	for _, id := range c.GroupIDs {
		if id <= 0 {
			return fmt.Errorf("managed Claude recovery group IDs must be positive")
		}
	}
	if c.MaxSummaryInputBytes < 4096 || c.MaxSummaryInputBytes > 2*1024*1024 || c.CheckpointEveryTurns < 1 || c.CheckpointEveryTurns > 20 || c.ContextWindowTokens < 8192 || c.ContextWindowTokens > 200000 || c.MaxHistoryBytes < 4096 || c.MaxHistoryBytes > 32*1024*1024 || c.MaxSummaryTokens < 256 || c.MaxSummaryTokens > 8192 || c.MaxSummaryCallsPerUserDay < 1 || c.MaxSummaryCallsPerUserDay > 1000 || c.RetentionHours < 1 || c.RetentionHours > 720 || c.LeaseSeconds < 30 || c.LeaseSeconds > 1800 || c.SummaryTimeoutSeconds < 5 || c.SummaryTimeoutSeconds > 120 {
		return fmt.Errorf("managed Claude recovery limits are outside supported bounds")
	}
	return nil
}
