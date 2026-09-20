package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Host                               string `json:"host"`
	Port                               int    `json:"port"`
	DataPath                           string `json:"data_path"`
	SessionTTLHours                    int    `json:"session_ttl_hours"`
	SecureCookie                       bool   `json:"secure_cookie"`
	APIKey                             string `json:"api_key"`
	PublicBaseURL                      string `json:"public_base_url"`
	ICloudDefaultHost                  string `json:"icloud_default_host"`
	ICloudClientID                     string `json:"icloud_client_id"`
	AppleAccountAPIKey                 string `json:"apple_account_api_key"`
	AppleAccountKeepAliveEnabled       bool   `json:"apple_account_keep_alive_enabled"`
	AppleAccountKeepAliveMS            int    `json:"apple_account_keep_alive_ms"`
	AppleAccountKeepAliveJitterPercent int    `json:"apple_account_keep_alive_jitter_percent"`
	MailWatcherEnabled                 bool   `json:"mail_watcher_enabled"`
	MailWatcherPollMS                  int    `json:"mail_watcher_poll_ms"`
	MailWatcherWebPollMS               int    `json:"mail_watcher_web_poll_ms"`
	MailWatcherFetchLimit              int    `json:"mail_watcher_fetch_limit"`
	MailWatcherInitialFetchLimit       int    `json:"mail_watcher_initial_fetch_limit"`
	MailWatcherLookbackHours           int    `json:"mail_watcher_lookback_hours"`
	PublicFastSyncWaitMS               int    `json:"public_fast_sync_wait_ms"`
	PublicSyncMinIntervalMS            int    `json:"public_sync_min_interval_ms"`
	PublicMailboxLeaseTTLMinutes       int    `json:"public_mailbox_lease_ttl_minutes"`
	PublicMailboxLeaseMaxTTLMinutes    int    `json:"public_mailbox_lease_max_ttl_minutes"`
	PublicMailboxLeaseSweepSeconds     int    `json:"public_mailbox_lease_sweep_seconds"`
	DatabaseBackupDir                  string `json:"database_backup_dir"`
	DatabaseBackupRetentionCount       int    `json:"database_backup_retention_count"`
	DatabaseMessageRetentionDays       int    `json:"database_message_retention_days"`
	DatabaseChangeLogLimit             int    `json:"database_change_log_limit"`
	UpdateEnabled                      bool   `json:"update_enabled"`
	UpdateRepository                   string `json:"update_repository"`
	ServerChanSendKey                  string `json:"server_chan_send_key"`
	ServerChanHideIP                   bool   `json:"server_chan_hide_ip"`
	ServerChanNotifyAdminLogin         bool   `json:"server_chan_notify_admin_login"`
	ServerChanNotifyLoginStateOffline  bool   `json:"server_chan_notify_login_state_offline"`
}

func Default() Config {
	return Config{
		Host:                               "127.0.0.1",
		Port:                               8788,
		DataPath:                           filepath.Join("data", "app.db"),
		SessionTTLHours:                    24 * 7,
		SecureCookie:                       false,
		ICloudDefaultHost:                  "www.icloud.com.cn",
		ICloudClientID:                     "d39ba9916b7251055b22c7f910e2ea796ee65e98b2ddecea8f5dde8d9d1a815d",
		AppleAccountKeepAliveEnabled:       true,
		AppleAccountKeepAliveMS:            180000,
		AppleAccountKeepAliveJitterPercent: 15,
		MailWatcherEnabled:                 true,
		MailWatcherPollMS:                  3000,
		MailWatcherWebPollMS:               60000,
		MailWatcherFetchLimit:              8,
		MailWatcherInitialFetchLimit:       20,
		MailWatcherLookbackHours:           24,
		PublicFastSyncWaitMS:               600,
		PublicSyncMinIntervalMS:            3000,
		PublicMailboxLeaseTTLMinutes:       30,
		PublicMailboxLeaseMaxTTLMinutes:    7 * 24 * 60,
		PublicMailboxLeaseSweepSeconds:     30,
		DatabaseBackupDir:                  filepath.Join("data", "backups"),
		DatabaseBackupRetentionCount:       3,
		DatabaseMessageRetentionDays:       90,
		DatabaseChangeLogLimit:             5000,
		UpdateEnabled:                      true,
		UpdateRepository:                   "1173887665/iCloud-Privacy-Mail-v2",
		ServerChanHideIP:                   true,
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	path = strings.TrimSpace(path)
	if path == "" {
		return cfg, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, nil
		}
		return Config{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return Config{}, err
	}
	var incoming Config
	if err := json.Unmarshal(data, &incoming); err != nil {
		return Config{}, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, err
	}
	if strings.TrimSpace(incoming.Host) != "" {
		cfg.Host = strings.TrimSpace(incoming.Host)
	}
	if incoming.Port > 0 {
		cfg.Port = incoming.Port
	}
	if strings.TrimSpace(incoming.DataPath) != "" {
		dataPath := strings.TrimSpace(incoming.DataPath)
		if !filepath.IsAbs(dataPath) {
			dataPath = filepath.Join(filepath.Dir(path), dataPath)
		}
		cfg.DataPath = filepath.Clean(dataPath)
	}
	if incoming.SessionTTLHours > 0 {
		cfg.SessionTTLHours = incoming.SessionTTLHours
	}
	cfg.SecureCookie = incoming.SecureCookie
	cfg.APIKey = strings.TrimSpace(incoming.APIKey)
	cfg.PublicBaseURL = strings.TrimRight(strings.TrimSpace(incoming.PublicBaseURL), "/")
	if strings.TrimSpace(incoming.ICloudDefaultHost) != "" {
		cfg.ICloudDefaultHost = strings.TrimSpace(incoming.ICloudDefaultHost)
	}
	if strings.TrimSpace(incoming.ICloudClientID) != "" {
		cfg.ICloudClientID = strings.TrimSpace(incoming.ICloudClientID)
	}
	cfg.AppleAccountAPIKey = strings.TrimSpace(incoming.AppleAccountAPIKey)
	if incoming.AppleAccountKeepAliveMS > 0 {
		cfg.AppleAccountKeepAliveMS = incoming.AppleAccountKeepAliveMS
	}
	if _, ok := raw["apple_account_keep_alive_jitter_percent"]; ok && incoming.AppleAccountKeepAliveJitterPercent >= 0 && incoming.AppleAccountKeepAliveJitterPercent <= 50 {
		cfg.AppleAccountKeepAliveJitterPercent = incoming.AppleAccountKeepAliveJitterPercent
	}
	if incoming.MailWatcherPollMS > 0 {
		cfg.MailWatcherPollMS = incoming.MailWatcherPollMS
	}
	if incoming.MailWatcherWebPollMS > 0 {
		cfg.MailWatcherWebPollMS = incoming.MailWatcherWebPollMS
	}
	if incoming.MailWatcherFetchLimit > 0 {
		cfg.MailWatcherFetchLimit = incoming.MailWatcherFetchLimit
	}
	if incoming.MailWatcherInitialFetchLimit > 0 {
		cfg.MailWatcherInitialFetchLimit = incoming.MailWatcherInitialFetchLimit
	}
	if incoming.MailWatcherLookbackHours > 0 {
		cfg.MailWatcherLookbackHours = incoming.MailWatcherLookbackHours
	}
	if incoming.PublicFastSyncWaitMS > 0 {
		cfg.PublicFastSyncWaitMS = incoming.PublicFastSyncWaitMS
	}
	if incoming.PublicSyncMinIntervalMS > 0 {
		cfg.PublicSyncMinIntervalMS = incoming.PublicSyncMinIntervalMS
	}
	if incoming.PublicMailboxLeaseTTLMinutes > 0 {
		cfg.PublicMailboxLeaseTTLMinutes = incoming.PublicMailboxLeaseTTLMinutes
	}
	if incoming.PublicMailboxLeaseMaxTTLMinutes > 0 {
		cfg.PublicMailboxLeaseMaxTTLMinutes = incoming.PublicMailboxLeaseMaxTTLMinutes
	}
	if incoming.PublicMailboxLeaseSweepSeconds > 0 {
		cfg.PublicMailboxLeaseSweepSeconds = incoming.PublicMailboxLeaseSweepSeconds
	}
	if strings.TrimSpace(incoming.DatabaseBackupDir) != "" {
		backupDir := strings.TrimSpace(incoming.DatabaseBackupDir)
		if !filepath.IsAbs(backupDir) {
			backupDir = filepath.Join(filepath.Dir(path), backupDir)
		}
		cfg.DatabaseBackupDir = filepath.Clean(backupDir)
	}
	if incoming.DatabaseBackupRetentionCount > 0 {
		cfg.DatabaseBackupRetentionCount = incoming.DatabaseBackupRetentionCount
	}
	if incoming.DatabaseMessageRetentionDays > 0 {
		cfg.DatabaseMessageRetentionDays = incoming.DatabaseMessageRetentionDays
	}
	if incoming.DatabaseChangeLogLimit >= 1000 {
		cfg.DatabaseChangeLogLimit = incoming.DatabaseChangeLogLimit
	}
	if cfg.PublicMailboxLeaseMaxTTLMinutes < cfg.PublicMailboxLeaseTTLMinutes {
		cfg.PublicMailboxLeaseMaxTTLMinutes = cfg.PublicMailboxLeaseTTLMinutes
	}
	if _, ok := raw["apple_account_keep_alive_enabled"]; ok {
		cfg.AppleAccountKeepAliveEnabled = incoming.AppleAccountKeepAliveEnabled
	}
	if _, ok := raw["mail_watcher_enabled"]; ok {
		cfg.MailWatcherEnabled = incoming.MailWatcherEnabled
	}
	if _, ok := raw["update_enabled"]; ok {
		cfg.UpdateEnabled = incoming.UpdateEnabled
	}
	if strings.TrimSpace(incoming.UpdateRepository) != "" {
		cfg.UpdateRepository = strings.TrimSpace(incoming.UpdateRepository)
	}
	cfg.ServerChanSendKey = strings.TrimSpace(incoming.ServerChanSendKey)
	if _, ok := raw["server_chan_hide_ip"]; ok {
		cfg.ServerChanHideIP = incoming.ServerChanHideIP
	}
	if _, ok := raw["server_chan_notify_admin_login"]; ok {
		cfg.ServerChanNotifyAdminLogin = incoming.ServerChanNotifyAdminLogin
	}
	if _, ok := raw["server_chan_notify_login_state_offline"]; ok {
		cfg.ServerChanNotifyLoginStateOffline = incoming.ServerChanNotifyLoginStateOffline
	}
	return cfg, nil
}
