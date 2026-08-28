package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServerChanSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	data := []byte(`{
		"server_chan_send_key":"SCT-config-test",
		"server_chan_hide_ip":false,
		"server_chan_notify_admin_login":true,
		"server_chan_notify_login_state_offline":true,
		"database_backup_retention_count":5
	}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("读取配置失败：%v", err)
	}
	if cfg.ServerChanSendKey != "SCT-config-test" || cfg.ServerChanHideIP || !cfg.ServerChanNotifyAdminLogin || !cfg.ServerChanNotifyLoginStateOffline || cfg.DatabaseBackupRetentionCount != 5 {
		t.Fatalf("Server 酱配置不正确：%+v", cfg)
	}
}

func TestDefaultDatabaseBackupRetentionCount(t *testing.T) {
	if count := Default().DatabaseBackupRetentionCount; count != 3 {
		t.Fatalf("默认备份保留数量为 %d，期望 3", count)
	}
}
