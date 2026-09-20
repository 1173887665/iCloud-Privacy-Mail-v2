package mailbox

import (
	"path/filepath"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/store"
)

func TestCachedCodeOnlyReturnsMessagesAfterLastServedMessage(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试数据库失败：%v", err)
	}
	defer state.Close()
	mailbox, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{Email: "code-order@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatalf("创建测试邮箱失败：%v", err)
	}
	now := time.Now().Add(-time.Minute)
	if _, err := state.ApplyMailboxSyncBatch([]store.MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []store.MailboxSyncMessage{
		{RemoteID: "imap:1", Source: "imap", Subject: "OpenAI 验证码 111111", Body: "验证码 111111", ReceivedAt: now},
		{RemoteID: "imap:2", Source: "imap", Subject: "OpenAI 验证码 222222", Body: "验证码 222222", ReceivedAt: now.Add(time.Second)},
	}}}); err != nil {
		t.Fatalf("保存测试邮件失败：%v", err)
	}
	service := NewService(config.Default(), state)

	first, found, err := service.CachedCodeWithQuery(mailbox.ID, CodeQuery{Keyword: "OpenAI", MarkAsServed: true})
	if err != nil || !found || first.Code != "222222" {
		t.Fatalf("首次取码结果不正确：result=%+v found=%t err=%v", first, found, err)
	}
	updated, _ := state.FindMailboxByID(mailbox.ID)
	second, found, err := service.CachedCodeWithQuery(mailbox.ID, CodeQuery{Keyword: "OpenAI", SkipMessageID: updated.LastCodeMessageID, MarkAsServed: true})
	if err != nil || found {
		t.Fatalf("没有新邮件时不应重新返回旧验证码：result=%+v found=%t err=%v", second, found, err)
	}

	if _, err := state.ApplyMailboxSyncBatch([]store.MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []store.MailboxSyncMessage{{
		RemoteID: "imap:3", Source: "imap", Subject: "OpenAI 验证码 333333", Body: "验证码 333333", ReceivedAt: now.Add(2 * time.Second),
	}}}}); err != nil {
		t.Fatalf("保存新验证码邮件失败：%v", err)
	}
	third, found, err := service.CachedCodeWithQuery(mailbox.ID, CodeQuery{Keyword: "OpenAI", SkipMessageID: updated.LastCodeMessageID, MarkAsServed: true})
	if err != nil || !found || third.Code != "333333" {
		t.Fatalf("新验证码邮件没有被返回：result=%+v found=%t err=%v", third, found, err)
	}
}

func TestCachedCodeAllowStaleReturnsServedMessage(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试数据库失败：%v", err)
	}
	defer state.Close()
	mailbox, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{Email: "code-stale@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatalf("创建测试邮箱失败：%v", err)
	}
	receivedAt := time.Now().Add(-10 * time.Minute)
	if _, err := state.ApplyMailboxSyncBatch([]store.MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []store.MailboxSyncMessage{{
		RemoteID: "imap:stale", Source: "imap", Subject: "OpenAI 验证码 654321", Body: "验证码 654321", ReceivedAt: receivedAt,
	}}}}); err != nil {
		t.Fatalf("保存测试邮件失败：%v", err)
	}
	messages := state.MessagesForMailbox(mailbox.ID)
	if len(messages) != 1 {
		t.Fatalf("测试邮件数量不正确：%d", len(messages))
	}
	if err := state.SetMailboxLastCode(mailbox.ID, messages[0].ID, time.Now()); err != nil {
		t.Fatalf("标记已取验证码失败：%v", err)
	}
	service := NewService(config.Default(), state)
	if _, found, err := service.CachedCodeWithQuery(mailbox.ID, CodeQuery{Keyword: "OpenAI", SkipMessageID: messages[0].ID}); err != nil || found {
		t.Fatalf("默认取码不应返回过期且已取验证码：found=%t err=%v", found, err)
	}
	result, found, err := service.CachedCodeWithQuery(mailbox.ID, CodeQuery{Keyword: "OpenAI", IncludeServed: true, AllowStale: true})
	if err != nil || !found || result.Code != "654321" {
		t.Fatalf("allow_stale 应返回本地已取验证码：result=%+v found=%t err=%v", result, found, err)
	}
}
