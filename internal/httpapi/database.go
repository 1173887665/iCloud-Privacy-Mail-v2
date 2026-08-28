package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (s *Server) handleDatabaseStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{
		"database":               s.store.DatabaseStatus(),
		"backup_dir":             s.cfg.DatabaseBackupDir,
		"backup_retention_count": s.cfg.DatabaseBackupRetentionCount,
		"message_retention_days": s.cfg.DatabaseMessageRetentionDays,
	}})
}

func (s *Server) handleDatabaseBackup(w http.ResponseWriter, _ *http.Request) {
	path, err := s.createDatabaseBackup()
	if err != nil {
		s.cleanupDatabaseBackups()
		_ = s.store.RecordMaintenance("backup", "failed", err.Error())
		writeError(w, http.StatusInternalServerError, "database_backup_failed", "创建数据库备份失败："+err.Error())
		return
	}
	s.cleanupDatabaseBackups()
	_ = s.store.RecordMaintenance("backup", "success", path)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"path": path}})
}

func (s *Server) handleDatabaseCheck(w http.ResponseWriter, _ *http.Request) {
	result, err := s.store.IntegrityCheck()
	if err != nil {
		_ = s.store.RecordMaintenance("integrity-check", "failed", err.Error())
		writeError(w, http.StatusInternalServerError, "database_check_failed", "数据库完整性检查失败："+err.Error())
		return
	}
	status := "success"
	if !strings.EqualFold(strings.TrimSpace(result), "ok") {
		status = "failed"
	}
	_ = s.store.RecordMaintenance("integrity-check", status, result)
	writeJSON(w, http.StatusOK, map[string]any{"success": status == "success", "data": map[string]any{"result": result}})
}

func (s *Server) handleDatabaseOptimize(w http.ResponseWriter, _ *http.Request) {
	if err := s.store.Checkpoint(); err != nil {
		writeError(w, http.StatusInternalServerError, "database_checkpoint_failed", "合并 WAL 失败："+err.Error())
		return
	}
	if err := s.store.Vacuum(); err != nil {
		_ = s.store.RecordMaintenance("optimize", "failed", err.Error())
		writeError(w, http.StatusInternalServerError, "database_optimize_failed", "回收数据库空间失败："+err.Error())
		return
	}
	_ = s.store.RecordMaintenance("optimize", "success", "已完成 WAL checkpoint 和 VACUUM")
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": map[string]any{"database": s.store.DatabaseStatus()}})
}

func (s *Server) runDatabaseMaintenance(ctx context.Context) {
	// 启动一分钟后执行首轮，之后每 24 小时执行一次。
	timer := time.NewTimer(time.Minute)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			s.performDatabaseMaintenance()
			timer.Reset(24 * time.Hour)
		}
	}
}

func (s *Server) performDatabaseMaintenance() {
	result, err := s.store.IntegrityCheck()
	if err != nil || !strings.EqualFold(strings.TrimSpace(result), "ok") {
		detail := result
		if err != nil {
			detail = err.Error()
		}
		_ = s.store.RecordMaintenance("automatic", "failed", detail)
		s.log.Error("数据库自动完整性检查异常", "详情", detail)
		return
	}
	deleted, err := s.store.PruneMessagesBefore(time.Now().AddDate(0, 0, -s.cfg.DatabaseMessageRetentionDays))
	if err != nil {
		s.log.Warn("清理过期邮件失败", "错误", err)
	}
	backupPath, backupErr := s.createDatabaseBackup()
	if checkpointErr := s.store.Checkpoint(); checkpointErr != nil {
		s.log.Warn("自动合并 SQLite WAL 失败", "错误", checkpointErr)
	}
	if backupErr != nil {
		s.cleanupDatabaseBackups()
		_ = s.store.RecordMaintenance("automatic", "failed", backupErr.Error())
		s.log.Warn("自动数据库备份失败", "错误", backupErr)
		return
	}
	s.cleanupDatabaseBackups()
	detail := fmt.Sprintf("备份=%s，清理过期邮件=%d，最多保留=%d份", backupPath, deleted, s.cfg.DatabaseBackupRetentionCount)
	_ = s.store.RecordMaintenance("automatic", "success", detail)
	s.log.Info("数据库自动维护完成", "备份", backupPath, "清理过期邮件", deleted, "最多保留", s.cfg.DatabaseBackupRetentionCount)
}

func (s *Server) createDatabaseBackup() (string, error) {
	dir := filepath.Clean(s.cfg.DatabaseBackupDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "app-"+time.Now().Format("20060102-150405.000")+".db")
	return path, s.store.BackupDatabase(path)
}

func (s *Server) cleanupDatabaseBackups() {
	dir := filepath.Clean(s.cfg.DatabaseBackupDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type backupPair struct {
		name    string
		modTime time.Time
	}
	names := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			names[entry.Name()] = true
		}
	}
	backups := make([]backupPair, 0, len(entries)/2)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, "app-") || !strings.HasSuffix(name, ".db") {
			continue
		}
		if !names[name+".key"] {
			_ = os.Remove(filepath.Join(dir, name))
			continue
		}
		info, err := entry.Info()
		if err == nil {
			backups = append(backups, backupPair{name: name, modTime: info.ModTime()})
		}
	}
	for name := range names {
		if strings.HasPrefix(name, "app-") && strings.HasSuffix(name, ".db.key") && !names[strings.TrimSuffix(name, ".key")] {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].modTime.Equal(backups[j].modTime) {
			return backups[i].name > backups[j].name
		}
		return backups[i].modTime.After(backups[j].modTime)
	})
	retentionCount := s.cfg.DatabaseBackupRetentionCount
	if retentionCount <= 0 {
		retentionCount = 3
	}
	if len(backups) <= retentionCount {
		return
	}
	for _, backup := range backups[retentionCount:] {
		path := filepath.Join(dir, backup.name)
		_ = os.Remove(path)
		_ = os.Remove(path + ".key")
	}
}
