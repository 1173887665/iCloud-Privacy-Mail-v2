package mailbox

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/protocol"
	"icloud-privacy-mail-v2/internal/store"
)

type remoteMailboxDeleteClientFixture struct {
	remote        protocol.ICloudRemoteMailbox
	operations    []string
	remoteIDs     []string
	listCalls     int
	moveErr       error
	emptyTrashErr error
	onDelete      func()
}

func (f *remoteMailboxDeleteClientFixture) ListPrivacyMailboxes(context.Context, protocol.ICloudSession) ([]protocol.ICloudRemoteMailbox, error) {
	f.operations = append(f.operations, "查询远端邮箱")
	f.listCalls++
	if f.listCalls == 1 {
		return []protocol.ICloudRemoteMailbox{f.remote}, nil
	}
	return nil, nil
}

func (f *remoteMailboxDeleteClientFixture) DeletePrivacyMailbox(_ context.Context, _ protocol.ICloudSession, anonymousID string) error {
	f.operations = append(f.operations, "删除远端邮箱")
	if anonymousID != f.remote.AnonymousID {
		return errors.New("远端邮箱标识不匹配")
	}
	if f.onDelete != nil {
		f.onDelete()
	}
	return nil
}

func (f *remoteMailboxDeleteClientFixture) MoveRemoteMessagesToTrash(_ context.Context, _ protocol.ICloudSession, remoteIDs []string) (protocol.ICloudMailCleanupResult, error) {
	f.operations = append(f.operations, "移动远端邮件")
	f.remoteIDs = append([]string(nil), remoteIDs...)
	if f.moveErr != nil {
		return protocol.ICloudMailCleanupResult{}, f.moveErr
	}
	return protocol.ICloudMailCleanupResult{
		MovedToTrash:   len(remoteIDs),
		MovedRemoteIDs: append([]string(nil), remoteIDs...),
	}, nil
}

func (f *remoteMailboxDeleteClientFixture) EmptyTrash(context.Context, protocol.ICloudSession) (int, error) {
	f.operations = append(f.operations, "清空远端废纸篓")
	if f.emptyTrashErr != nil {
		return 0, f.emptyTrashErr
	}
	return 1, nil
}

func TestDeleteCompletelyUsesSyncedMessageCleanupBeforeDeletingMailbox(t *testing.T) {
	state, mailbox := newDeleteServiceFixture(t)
	client := &remoteMailboxDeleteClientFixture{
		remote: protocol.ICloudRemoteMailbox{AnonymousID: mailbox.AnonymousID, Email: mailbox.Email},
	}
	client.onDelete = func() {
		if _, ok := state.FindMailboxByID(mailbox.ID); !ok {
			t.Error("删除 Apple 远端邮箱时，本地邮箱记录应仍然存在")
		}
		if messages := state.MessagesForMailbox(mailbox.ID); len(messages) != 0 {
			t.Errorf("删除 Apple 远端邮箱前本地邮件尚未清空：%d 封", len(messages))
		}
	}
	service := NewService(config.Config{}, state)
	service.deleteClient = client

	if err := service.DeleteCompletely(context.Background(), mailbox.ID); err != nil {
		t.Fatalf("彻底删除邮箱失败：%v", err)
	}
	expected := []string{"移动远端邮件", "清空远端废纸篓", "查询远端邮箱", "删除远端邮箱", "查询远端邮箱"}
	if !reflect.DeepEqual(client.operations, expected) {
		t.Fatalf("删除执行顺序不正确：得到 %v，期望 %v", client.operations, expected)
	}
	if !reflect.DeepEqual(client.remoteIDs, []string{"icloud:Inbox:101"}) {
		t.Fatalf("远端邮件标识不正确：%v", client.remoteIDs)
	}
	if _, ok := state.FindMailboxByID(mailbox.ID); ok {
		t.Fatal("完成邮件清理和 Apple 删除后，本地邮箱记录仍然存在")
	}
}

func TestDeleteCompletelyStopsWhenMovingRemoteMessagesFails(t *testing.T) {
	state, mailbox := newDeleteServiceFixture(t)
	client := &remoteMailboxDeleteClientFixture{
		remote:  protocol.ICloudRemoteMailbox{AnonymousID: mailbox.AnonymousID, Email: mailbox.Email},
		moveErr: errors.New("远端邮件清理测试失败"),
	}
	service := NewService(config.Config{}, state)
	service.deleteClient = client

	err := service.DeleteCompletely(context.Background(), mailbox.ID)
	if err == nil || !strings.Contains(err.Error(), "删除隐私邮箱前清理已同步的 Apple 远端邮件失败") {
		t.Fatalf("未返回明确的邮件清理错误：%v", err)
	}
	if !reflect.DeepEqual(client.operations, []string{"移动远端邮件"}) {
		t.Fatalf("清理失败后仍执行了后续步骤：%v", client.operations)
	}
	if _, ok := state.FindMailboxByID(mailbox.ID); !ok {
		t.Fatal("远端邮件清理失败时不应删除邮箱")
	}
	if messages := state.MessagesForMailbox(mailbox.ID); len(messages) != 2 {
		t.Fatalf("远端邮件清理失败时不应清空本地邮件：剩余 %d 封", len(messages))
	}
}

func TestDeleteCompletelyKeepsLocalDataWhenEmptyTrashFails(t *testing.T) {
	state, mailbox := newDeleteServiceFixture(t)
	client := &remoteMailboxDeleteClientFixture{
		remote:        protocol.ICloudRemoteMailbox{AnonymousID: mailbox.AnonymousID, Email: mailbox.Email},
		emptyTrashErr: errors.New("清空废纸篓测试失败"),
	}
	service := NewService(config.Config{}, state)
	service.deleteClient = client

	err := service.DeleteCompletely(context.Background(), mailbox.ID)
	if err == nil || !strings.Contains(err.Error(), "清空废纸篓测试失败") {
		t.Fatalf("未返回清空废纸篓错误：%v", err)
	}
	if !reflect.DeepEqual(client.operations, []string{"移动远端邮件", "清空远端废纸篓"}) {
		t.Fatalf("清空废纸篓失败后仍执行了后续步骤：%v", client.operations)
	}
	if _, ok := state.FindMailboxByID(mailbox.ID); !ok {
		t.Fatal("清空废纸篓失败时本地邮箱记录应保留")
	}
	if messages := state.MessagesForMailbox(mailbox.ID); len(messages) != 2 {
		t.Fatalf("清空废纸篓失败时本地邮件应保留：剩余 %d 封", len(messages))
	}
}

func TestCleanRemoteMessagesUsesSharedSyncedMessageCleanup(t *testing.T) {
	state, mailbox := newDeleteServiceFixture(t)
	client := &remoteMailboxDeleteClientFixture{}
	service := NewService(config.Config{}, state)
	service.deleteClient = client

	result, err := service.CleanRemoteMessages(context.Background(), mailbox.ID, RemoteCleanupOptions{
		MoveSynced: true,
		EmptyTrash: true,
	})
	if err != nil {
		t.Fatalf("清理 Apple 远端邮件失败：%v", err)
	}
	if !reflect.DeepEqual(client.operations, []string{"移动远端邮件", "清空远端废纸篓"}) {
		t.Fatalf("详情清理执行顺序不正确：%v", client.operations)
	}
	if result.MovedToTrash != 1 || result.Destroyed != 1 || result.LocalRemoved != 1 {
		t.Fatalf("详情清理结果不正确：%+v", result)
	}
	if _, ok := state.FindMailboxByID(mailbox.ID); !ok {
		t.Fatal("详情清理后隐私邮箱记录应保留")
	}
	if messages := state.MessagesForMailbox(mailbox.ID); len(messages) != 1 || messages[0].RemoteID != "" {
		t.Fatalf("详情清理后应仅保留缺少远端标识的本地邮件：%+v", messages)
	}
}

func TestRemoteMailCleanupStopsWhenWebAPIDisabled(t *testing.T) {
	state, mailbox := newDeleteServiceFixture(t)
	settings := state.Settings()
	settings.EnableWebRemoteMailCleanup = false
	if _, err := state.SaveSettings(settings); err != nil {
		t.Fatalf("关闭 Web API 远端邮件操作失败：%v", err)
	}
	client := &remoteMailboxDeleteClientFixture{}
	service := NewService(config.Config{}, state)
	service.deleteClient = client

	_, err := service.CleanRemoteMessages(context.Background(), mailbox.ID, RemoteCleanupOptions{MoveSynced: true})
	if err == nil || !strings.Contains(err.Error(), "远端邮件操作已关闭") {
		t.Fatalf("关闭 Web API 后的远端清理提示不正确：%v", err)
	}
	if len(client.operations) != 0 {
		t.Fatalf("关闭 Web API 后仍执行了远端操作：%v", client.operations)
	}
}

func TestCleanRemoteMailboxesPurgesAllLocalMessages(t *testing.T) {
	state, mailbox := newDeleteServiceFixture(t)
	client := &remoteMailboxDeleteClientFixture{}
	service := NewService(config.Config{}, state)
	service.deleteClient = client

	result, err := service.CleanRemoteMailboxes(context.Background(), RemoteCleanupOptions{
		MoveSynced: true,
		EmptyTrash: true,
		PurgeLocal: true,
	})
	if err != nil {
		t.Fatalf("批量清理邮件失败：%v", err)
	}

	if result.FailedMailboxes != 0 {
		t.Fatalf("全部清理出现失败：%+v", result.Failures)
	}
	if result.Mailboxes != 1 {
		t.Fatalf("处理邮箱数量不正确：得到 %d，期望 1", result.Mailboxes)
	}
	if result.Cleanup.LocalRemoved != 2 {
		t.Fatalf("本地邮件清理数量不正确：得到 %d，期望 2", result.Cleanup.LocalRemoved)
	}
	if messages := state.MessagesForMailbox(mailbox.ID); len(messages) != 0 {
		t.Fatalf("全部清理后仍有本地邮件：%d 封", len(messages))
	}
	if !reflect.DeepEqual(client.remoteIDs, []string{"icloud:Inbox:101"}) {
		t.Fatalf("远端邮件标识不正确：%v", client.remoteIDs)
	}
}

func newDeleteServiceFixture(t *testing.T) (*store.Store, domain.Mailbox) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建临时状态失败：%v", err)
	}
	session, err := state.SaveICloudSession(domain.ICloudSession{
		AppleID: "delete-fixture@icloud.com",
		DSID:    "fixture-dsid",
		Cookies: []domain.SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "fixture-token"}},
	})
	if err != nil {
		t.Fatalf("创建测试 Apple 登录态失败：%v", err)
	}
	mailbox, _, err := state.UpsertMailboxFromRemote(session.AccountID, domain.RemoteMailbox{
		AnonymousID: "anonymous-delete-fixture",
		Email:       "delete-fixture@icloud.com",
		Label:       "delete_fixture",
		IsActive:    true,
	}, "删除流程测试")
	if err != nil {
		t.Fatalf("创建测试邮箱失败：%v", err)
	}
	if _, _, err := state.UpsertMessage(mailbox.ID, "icloud:Inbox:101", "icloud", "验证码 101", "sender@example.com", "101", time.Now()); err != nil {
		t.Fatalf("保存远端测试邮件失败：%v", err)
	}
	if _, _, err := state.UpsertMessage(mailbox.ID, "", "local", "本地缓存", "sender@example.com", "local", time.Now()); err != nil {
		t.Fatalf("保存本地测试邮件失败：%v", err)
	}
	return state, mailbox
}
