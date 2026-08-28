package httpapi

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/store"
)

func TestAutomaticDatabaseMaintenanceCreatesBackup(t *testing.T) {
	server, backupDir := newDatabaseMaintenanceTestFixture(t)

	server.performDatabaseMaintenance()

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("读取备份目录失败：%v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("自动维护应创建数据库和密钥两个文件：%+v", entries)
	}
}

func TestManualDatabaseBackupCreatesDatabaseAndKey(t *testing.T) {
	server, backupDir := newDatabaseMaintenanceTestFixture(t)
	recorder := httptest.NewRecorder()

	server.handleDatabaseBackup(recorder, httptest.NewRequest(http.MethodPost, "/api/database/backup", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("手动备份失败：%d %s", recorder.Code, recorder.Body.String())
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("读取手动备份目录失败：%v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("手动备份应生成数据库和密钥两个文件：%+v", entries)
	}
	var databaseFound, keyFound bool
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".db" {
			databaseFound = true
		}
		if filepath.Ext(entry.Name()) == ".key" {
			keyFound = true
		}
	}
	if !databaseFound || !keyFound {
		t.Fatalf("手动备份文件不完整：%+v", entries)
	}
}

func TestDatabaseBackupCleanupKeepsLatestThreePairs(t *testing.T) {
	server, backupDir := newDatabaseMaintenanceTestFixture(t)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Now().Add(-time.Hour)
	for index := 1; index <= 5; index++ {
		name := fmt.Sprintf("app-test-%d.db", index)
		path := filepath.Join(backupDir, name)
		if err := os.WriteFile(path, []byte("database"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".key", []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
		modTime := baseTime.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path+".key", modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}

	server.cleanupDatabaseBackups()

	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 6 {
		t.Fatalf("应只保留最新三组备份：%+v", entries)
	}
	for _, index := range []int{1, 2} {
		path := filepath.Join(backupDir, fmt.Sprintf("app-test-%d.db", index))
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("旧备份未清理：%s", path)
		}
		if _, err := os.Stat(path + ".key"); !os.IsNotExist(err) {
			t.Fatalf("旧备份密钥未清理：%s.key", path)
		}
	}
}

func newDatabaseMaintenanceTestFixture(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	state, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("创建测试数据库失败：%v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	cfg := config.Default()
	cfg.DatabaseBackupDir = filepath.Join(dir, "backups")
	server := New(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return server, cfg.DatabaseBackupDir
}
