package store

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/domain"
)

func TestSQLiteInitializesEmptyDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	state, err := Open(path)
	if err != nil {
		t.Fatalf("初始化 SQLite 数据库失败：%v", err)
	}
	defer state.Close()
	var migrations int
	if err := state.db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&migrations); err != nil || migrations != databaseSchemaVersion {
		t.Fatalf("数据库迁移版本不正确：count=%d err=%v", migrations, err)
	}
	var journalMode string
	if err := state.db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil || journalMode != "wal" {
		t.Fatalf("SQLite WAL 未启用：mode=%q err=%v", journalMode, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取数据库权限失败：%v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("数据库权限为 %o，期望 600", info.Mode().Perm())
	}
	keyInfo, err := os.Stat(path + ".key")
	if err != nil || keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("数据库密钥权限不正确：info=%v err=%v", keyInfo, err)
	}
}

func TestSQLiteDoesNotImportLegacyStateJSON(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(legacyPath, []byte(`{"mailboxes":[{"id":"legacy"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	state, err := Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatalf("创建数据库失败：%v", err)
	}
	defer state.Close()
	if mailboxes := state.AllMailboxes(); len(mailboxes) != 0 {
		t.Fatalf("不应自动导入 state.json：%+v", mailboxes)
	}
	data, _ := os.ReadFile(legacyPath)
	if !bytes.Contains(data, []byte("legacy")) {
		t.Fatal("旧文件不应被运行服务改写")
	}
}

func TestSQLiteEncryptsSensitiveFieldsAndReopens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	state, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	mailbox, _, err := state.UpsertMailboxFromRemote("account", domain.RemoteMailbox{Email: "secret@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	settings := state.Settings()
	settings.PublicAPIKey = "public-secret"
	settings.ServerChanSendKey = "SCT-server-chan-secret"
	if _, err := state.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := state.db.QueryRow(`SELECT data_json FROM mailboxes WHERE id = ?`, mailbox.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(mailbox.APIToken)) || !bytes.Contains(raw, []byte(secretPrefix)) {
		t.Fatalf("邮箱 API Token 未加密：%s", raw)
	}
	if err := state.db.QueryRow(`SELECT data_json FROM settings WHERE id = 'system'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(settings.ServerChanSendKey)) || !bytes.Contains(raw, []byte(secretPrefix)) {
		t.Fatalf("Server 酱 SendKey 未加密：%s", raw)
	}
	if err := state.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	persisted, ok := reopened.FindMailboxByID(mailbox.ID)
	if !ok || persisted.APIToken != mailbox.APIToken || reopened.Settings().PublicAPIKey != "public-secret" || reopened.Settings().ServerChanSendKey != "SCT-server-chan-secret" {
		t.Fatalf("敏感字段解密结果不正确：%+v", persisted)
	}
}

func TestMailboxSyncBatchSkipsUnchangedWritesAndCollapsesChanges(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	first, _, _ := state.UpsertMailboxFromRemote("account", domain.RemoteMailbox{Email: "first@icloud.com", IsActive: true}, "")
	second, _, _ := state.UpsertMailboxFromRemote("account", domain.RemoteMailbox{Email: "second@icloud.com", IsActive: true}, "")
	now := time.Now()
	created, err := state.ApplyMailboxSyncBatch([]MailboxSyncUpdate{
		{MailboxID: first.ID, LastUID: "10", SyncedAt: now, Messages: []MailboxSyncMessage{{RemoteID: "10", Subject: "验证码", Body: "123456", ReceivedAt: now}}},
		{MailboxID: second.ID, LastUID: "10", SyncedAt: now},
	})
	if err != nil || created != 1 {
		t.Fatalf("批量同步失败：created=%d err=%v", created, err)
	}
	var batchChanges int
	if err := state.db.QueryRow(`SELECT COUNT(*) FROM change_log WHERE event_type = 'mailbox.batch-updated'`).Scan(&batchChanges); err != nil || batchChanges != 1 {
		t.Fatalf("批量 SSE 数量不正确：count=%d err=%v", batchChanges, err)
	}
	var before string
	_ = state.db.QueryRow(`SELECT updated_at FROM mailboxes WHERE id = ?`, first.ID).Scan(&before)
	created, err = state.ApplyMailboxSyncBatch([]MailboxSyncUpdate{{MailboxID: first.ID, LastUID: "10", SyncedAt: now.Add(10 * time.Second)}, {MailboxID: second.ID, LastUID: "10", SyncedAt: now.Add(10 * time.Second)}})
	if err != nil || created != 0 {
		t.Fatalf("无变化同步失败：created=%d err=%v", created, err)
	}
	var after string
	_ = state.db.QueryRow(`SELECT updated_at FROM mailboxes WHERE id = ?`, first.ID).Scan(&after)
	if before != after {
		t.Fatalf("无变化同步仍写入邮箱：before=%s after=%s", before, after)
	}
}

func TestMessageContentBackfillUpdatesExistingMessage(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	mailbox, _, err := state.UpsertMailboxFromRemote("account", domain.RemoteMailbox{Email: "html@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	created, err := state.ApplyMailboxSyncBatch([]MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []MailboxSyncMessage{{RemoteID: "imap:42", Source: "imap", Subject: "验证码", Body: "旧正文", ReceivedAt: now}}}})
	if err != nil || created != 1 {
		t.Fatalf("保存初始邮件失败：created=%d err=%v", created, err)
	}
	if _, err := state.db.Exec(`UPDATE messages SET data_json = json_remove(data_json, '$.content_type')`); err != nil {
		t.Fatalf("准备旧邮件数据失败：%v", err)
	}
	missing := state.MessagesMissingContent(0)
	if len(missing) != 1 {
		t.Fatalf("缺少正文类型的旧邮件数量不正确：%d", len(missing))
	}
	updated, err := state.ApplyMessageContentUpdates([]MessageContentUpdate{{MailboxID: mailbox.ID, MessageID: missing[0].ID, Body: "完整纯文本", HTMLBody: "<p>完整正文</p>", ContentType: "text/html"}})
	if err != nil || updated != 1 {
		t.Fatalf("补全已有邮件失败：updated=%d err=%v", updated, err)
	}
	messages := state.MessagesForMailbox(mailbox.ID)
	if len(messages) != 1 || messages[0].Body != "完整纯文本" || messages[0].HTMLBody != "<p>完整正文</p>" || messages[0].ContentType != "text/html" {
		t.Fatalf("邮件正文补全结果不正确：%+v", messages)
	}
	storedMailbox, _ := state.FindMailboxByID(mailbox.ID)
	if storedMailbox.ReceiveCount != 1 {
		t.Fatalf("补全正文不应重复增加收件数：%d", storedMailbox.ReceiveCount)
	}
	if remaining := state.MessagesMissingContent(0); len(remaining) != 0 {
		t.Fatalf("补全后仍有缺少正文类型的邮件：%d", len(remaining))
	}
}

func TestSQLiteUsesNativeMailboxPagination(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	for _, email := range []string{"one@icloud.com", "two@icloud.com", "three@icloud.com"} {
		if _, _, err := state.UpsertMailboxFromRemote("account", domain.RemoteMailbox{Email: email, Label: strings.TrimSuffix(email, "@icloud.com"), IsActive: true}, ""); err != nil {
			t.Fatal(err)
		}
	}
	page := state.Mailboxes("two", domain.StatusAvailable, "account", 1, 2)
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Email != "two@icloud.com" {
		t.Fatalf("SQL 分页筛选不正确：%+v", page)
	}
}
