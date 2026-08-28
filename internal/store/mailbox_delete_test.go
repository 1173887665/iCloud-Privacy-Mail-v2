package store

import (
	"path/filepath"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/domain"
)

func TestDeleteMailboxMessagesClearsOnlyTargetMailbox(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建临时状态失败：%v", err)
	}
	target, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{
		Email:    "delete-target@icloud.com",
		Label:    "delete_target",
		IsActive: true,
	}, "删除测试邮箱")
	if err != nil {
		t.Fatalf("创建待清理邮箱失败：%v", err)
	}
	other, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{
		Email:    "delete-other@icloud.com",
		Label:    "delete_other",
		IsActive: true,
	}, "其他测试邮箱")
	if err != nil {
		t.Fatalf("创建其他邮箱失败：%v", err)
	}

	first, _, err := state.UpsertMessage(target.ID, "icloud:Inbox:101", "icloud", "验证码 101", "sender@example.com", "101", time.Now())
	if err != nil {
		t.Fatalf("保存第一封测试邮件失败：%v", err)
	}
	if _, _, err := state.UpsertMessage(target.ID, "", "local", "本地邮件", "sender@example.com", "local", time.Now()); err != nil {
		t.Fatalf("保存第二封测试邮件失败：%v", err)
	}
	if _, _, err := state.UpsertMessage(other.ID, "icloud:Inbox:202", "icloud", "保留邮件", "sender@example.com", "202", time.Now()); err != nil {
		t.Fatalf("保存其他邮箱邮件失败：%v", err)
	}
	if err := state.SetMailboxLastCode(target.ID, first.ID, time.Now()); err != nil {
		t.Fatalf("保存最近验证码邮件失败：%v", err)
	}

	removed, err := state.DeleteMailboxMessages(target.ID)
	if err != nil {
		t.Fatalf("清空本地邮件失败：%v", err)
	}
	if removed != 2 {
		t.Fatalf("清理数量不正确：得到 %d，期望 2", removed)
	}
	if messages := state.MessagesForMailbox(target.ID); len(messages) != 0 {
		t.Fatalf("目标邮箱仍有本地邮件：%d 封", len(messages))
	}
	if messages := state.MessagesForMailbox(other.ID); len(messages) != 1 {
		t.Fatalf("其他邮箱邮件被误删：剩余 %d 封", len(messages))
	}
	stored, ok := state.FindMailboxByID(target.ID)
	if !ok {
		t.Fatal("清理邮件时不应删除邮箱记录")
	}
	if stored.ReceiveCount != 0 || stored.LastCodeMessageID != "" || !stored.LastCodeAt.IsZero() {
		t.Fatalf("邮箱邮件元数据未清空：%+v", stored)
	}
}

func TestMailboxSyncMergesDualPathRemoteIDs(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建临时状态失败：%v", err)
	}
	defer state.Close()
	mailbox, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{Email: "dual-path@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatalf("创建测试邮箱失败：%v", err)
	}
	receivedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	canonicalID := "message-id:dual-path@example.com"
	created, err := state.ApplyMailboxSyncBatch([]MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []MailboxSyncMessage{{
		RemoteID: "icloud:INBOX:42", RemoteIDs: []string{"icloud:INBOX:42"}, CanonicalID: canonicalID,
		Source: "icloud", Subject: "验证码", From: "sender@example.com", Body: "123456", ReceivedAt: receivedAt,
	}}}})
	if err != nil || created != 1 {
		t.Fatalf("保存 Web API 邮件失败：created=%d err=%v", created, err)
	}
	created, err = state.ApplyMailboxSyncBatch([]MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []MailboxSyncMessage{{
		RemoteID: "imap:99", RemoteIDs: []string{"imap:99"}, CanonicalID: canonicalID,
		Source: "imap", Subject: "验证码", From: "sender@example.com", Body: "完整正文 123456", HTMLBody: "<p>123456</p>", ContentType: "text/html", ReceivedAt: receivedAt,
	}}}})
	if err != nil || created != 0 {
		t.Fatalf("同一邮件的 IMAP 路径不应重复新增：created=%d err=%v", created, err)
	}
	messages := state.MessagesForMailbox(mailbox.ID)
	if len(messages) != 1 || len(messages[0].RemoteIDs) != 2 || messages[0].HTMLBody != "<p>123456</p>" {
		t.Fatalf("跨路径邮件合并结果不正确：%+v", messages)
	}
	storedMailbox, _ := state.FindMailboxByID(mailbox.ID)
	if storedMailbox.ReceiveCount != 1 {
		t.Fatalf("跨路径合并不应重复增加收件数：%d", storedMailbox.ReceiveCount)
	}
	removed, err := state.DeleteMailboxMessagesByRemoteIDs(mailbox.ID, []string{"imap:99"})
	if err != nil || removed != 1 || len(state.MessagesForMailbox(mailbox.ID)) != 0 {
		t.Fatalf("通过合并后的 IMAP 远端标识删除失败：removed=%d err=%v", removed, err)
	}
}

func TestMailboxSyncPreservesForwardToEmail(t *testing.T) {
	state, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建临时状态失败：%v", err)
	}
	defer state.Close()
	mailbox, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{
		Email: "forward-test@icloud.com", ForwardToEmail: "Primary@iCloud.com", IsActive: true,
	}, "")
	if err != nil || mailbox.ForwardToEmail != "primary@icloud.com" {
		t.Fatalf("转发主号保存错误：mailbox=%+v err=%v", mailbox, err)
	}
	updated, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{
		Email: "forward-test@icloud.com", IsActive: true,
	}, "")
	if err != nil || updated.ForwardToEmail != "primary@icloud.com" {
		t.Fatalf("远端未返回主号时不应清空已有值：mailbox=%+v err=%v", updated, err)
	}
}
