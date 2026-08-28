package mailbox

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/protocol"
	"icloud-privacy-mail-v2/internal/store"
)

type fakeMessageSyncBackend struct {
	mu          sync.Mutex
	imapCalls   int
	webCalls    int
	activeCalls int
	maxActive   int
	delay       time.Duration
	imapResult  protocol.MailSyncBatchResult
	webResult   protocol.MailSyncBatchResult
	imapErr     error
	webErr      error
	imapOptions []protocol.MailSyncOptions
	webOptions  []protocol.MailSyncOptions
}

func (backend *fakeMessageSyncBackend) SyncIMAP(ctx context.Context, _ protocol.LoginState, _ []domain.Mailbox, options protocol.MailSyncOptions) (protocol.MailSyncBatchResult, error) {
	backend.beginCall(true)
	backend.mu.Lock()
	backend.imapOptions = append(backend.imapOptions, options)
	backend.mu.Unlock()
	defer backend.endCall()
	if err := backend.wait(ctx); err != nil {
		return protocol.MailSyncBatchResult{}, err
	}
	return backend.imapResult, backend.imapErr
}

func (backend *fakeMessageSyncBackend) SyncWeb(ctx context.Context, _ protocol.ICloudSession, _ []domain.Mailbox, options protocol.MailSyncOptions) (protocol.MailSyncBatchResult, error) {
	backend.beginCall(false)
	backend.mu.Lock()
	backend.webOptions = append(backend.webOptions, options)
	backend.mu.Unlock()
	defer backend.endCall()
	if err := backend.wait(ctx); err != nil {
		return protocol.MailSyncBatchResult{}, err
	}
	return backend.webResult, backend.webErr
}

func (backend *fakeMessageSyncBackend) beginCall(imap bool) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if imap {
		backend.imapCalls++
	} else {
		backend.webCalls++
	}
	backend.activeCalls++
	if backend.activeCalls > backend.maxActive {
		backend.maxActive = backend.activeCalls
	}
}

func (backend *fakeMessageSyncBackend) endCall() {
	backend.mu.Lock()
	backend.activeCalls--
	backend.mu.Unlock()
}

func (backend *fakeMessageSyncBackend) wait(ctx context.Context) error {
	if backend.delay <= 0 {
		return nil
	}
	timer := time.NewTimer(backend.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (backend *fakeMessageSyncBackend) counts() (imap, web, maxActive int) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	return backend.imapCalls, backend.webCalls, backend.maxActive
}

func (backend *fakeMessageSyncBackend) latestOptions() (protocol.MailSyncOptions, protocol.MailSyncOptions) {
	backend.mu.Lock()
	defer backend.mu.Unlock()
	var imapOptions, webOptions protocol.MailSyncOptions
	if len(backend.imapOptions) > 0 {
		imapOptions = backend.imapOptions[len(backend.imapOptions)-1]
	}
	if len(backend.webOptions) > 0 {
		webOptions = backend.webOptions[len(backend.webOptions)-1]
	}
	return imapOptions, webOptions
}

func TestSyncExistingMailboxMessagesReturnsEmptyResult(t *testing.T) {
	state := openSyncTestStore(t)

	result, err := NewService(config.Default(), state).SyncExistingMailboxMessages(context.Background())
	if err != nil {
		t.Fatalf("空邮箱同步失败：%v", err)
	}
	if result.TotalAccounts != 0 || result.TotalMailboxes != 0 || result.SyncedMessages != 0 || len(result.Failures) != 0 {
		t.Fatalf("空邮箱同步结果不正确：%+v", result)
	}
}

func TestSyncExistingMailboxMessagesContinuesAfterAccountFailures(t *testing.T) {
	state := openSyncTestStore(t)
	for _, fixture := range []struct {
		accountID string
		email     string
	}{
		{accountID: "account-1", email: "first@icloud.com"},
		{accountID: "account-2", email: "second@icloud.com"},
	} {
		if _, _, err := state.UpsertMailboxFromRemote(fixture.accountID, domain.RemoteMailbox{Email: fixture.email, IsActive: true}, ""); err != nil {
			t.Fatalf("创建测试邮箱失败：%v", err)
		}
	}

	result, err := NewService(config.Default(), state).SyncExistingMailboxMessages(context.Background())
	if err != nil {
		t.Fatalf("批量同步不应因单个账号失败而中断：%v", err)
	}
	if result.TotalAccounts != 2 || result.TotalMailboxes != 2 || result.FailedAccounts != 2 || len(result.Failures) != 2 {
		t.Fatalf("批量同步失败统计不正确：%+v", result)
	}
	for _, failure := range result.Failures {
		if !strings.Contains(failure.Error, "登录态不存在") {
			t.Fatalf("失败原因不正确：%+v", failure)
		}
	}
}

func TestSyncMailboxBatchUsesIMAPFirst(t *testing.T) {
	state := openSyncTestStore(t)
	mailbox := saveSyncTestAccount(t, state, "primary@icloud.com", true)
	backend := &fakeMessageSyncBackend{imapResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}, Scanned: 3}}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncMailboxBatchWithOptions(context.Background(), []domain.Mailbox{mailbox}, MessageSyncOptions{
		Mode: protocol.MailSyncModeVerification, Limit: 20, UseCursor: true, AllowFallback: true,
	})
	if err != nil || result.IMAPAccounts != 1 || result.WebAPIAccounts != 0 {
		t.Fatalf("IMAP 优先同步结果不正确：结果=%+v，错误=%v", result, err)
	}
	if result.Accounts[0].Method != "imap" || result.Accounts[0].Mailboxes != 1 || mailbox.AccountID != result.Accounts[0].AccountID {
		t.Fatalf("IMAP 账号统计不正确：%+v", result.Accounts[0])
	}
	if imap, web, _ := backend.counts(); imap != 1 || web != 0 {
		t.Fatalf("IMAP 成功后不应调用 Web API：IMAP=%d，Web=%d", imap, web)
	}
}

func TestSyncExistingMailboxMessagesComplementsIMAPWithWeb(t *testing.T) {
	state := openSyncTestStore(t)
	mailbox := saveSyncTestAccount(t, state, "complement@icloud.com", true)
	receivedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	backend := &fakeMessageSyncBackend{
		imapResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}, Scanned: 20},
		webResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{
			mailbox.ID: {{RemoteID: "icloud:INBOX:42", Source: "icloud", CanonicalID: "message-id:web-only@example.com", Subject: "历史邮件", Body: "Web 正文", ReceivedAt: receivedAt}},
		}, Scanned: 20, Matched: 1},
	}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.IMAPAccounts != 1 || result.WebAPIAccounts != 1 || result.SyncedMessages != 1 || result.Matched != 1 {
		t.Fatalf("IMAP 与 Web 补查结果不正确：结果=%+v，错误=%v", result, err)
	}
	if imap, web, _ := backend.counts(); imap != 1 || web != 1 {
		t.Fatalf("手动同步应对每个账号各调用一次 IMAP 和 Web：IMAP=%d，Web=%d", imap, web)
	}
	imapOptions, webOptions := backend.latestOptions()
	if !imapOptions.FullScan || !webOptions.FullScan || imapOptions.Limit != 0 || webOptions.Limit != 0 || imapOptions.UseCursor || webOptions.UseCursor {
		t.Fatalf("批量同步应对两个路径执行无游标全量扫描：IMAP=%+v，Web=%+v", imapOptions, webOptions)
	}
}

func TestSyncMailboxMessagesUsesSharedDualPathForAllNewMail(t *testing.T) {
	state := openSyncTestStore(t)
	mailbox := saveSyncTestAccount(t, state, "single@icloud.com", true)
	backend := &fakeMessageSyncBackend{
		imapResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}},
		webResult:  protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}},
	}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncMailboxMessages(context.Background(), mailbox.ID)
	if err != nil || result.IMAPAccounts != 1 || result.WebAPIAccounts != 1 {
		t.Fatalf("单邮箱同步未复用双路径：结果=%+v，错误=%v", result, err)
	}
	imapOptions, webOptions := backend.latestOptions()
	if imapOptions.Mode != protocol.MailSyncModeAllRecent || webOptions.Mode != protocol.MailSyncModeAllRecent || !imapOptions.UseCursor || !webOptions.UseCursor || imapOptions.FullScan || webOptions.FullScan {
		t.Fatalf("单邮箱同步参数不正确：IMAP=%+v，Web=%+v", imapOptions, webOptions)
	}
}

func TestManualMailboxSyncReportsWebComplementFailure(t *testing.T) {
	state := openSyncTestStore(t)
	mailbox := saveSyncTestAccount(t, state, "partial@icloud.com", true)
	backend := &fakeMessageSyncBackend{
		imapResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}},
		webErr:     errors.New("Web 补查测试故障"),
	}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncMailboxMessages(context.Background(), mailbox.ID)
	if err == nil || !strings.Contains(err.Error(), "Web 补查测试故障") || result.IMAPAccounts != 1 || result.WebAPIAccounts != 0 {
		t.Fatalf("手动同步应报告 Web 补查失败并保留 IMAP 结果：结果=%+v，错误=%v", result, err)
	}
}

func TestBulkMailboxSyncKeepsIMAPSuccessWhenWebComplementFails(t *testing.T) {
	state := openSyncTestStore(t)
	saveSyncTestAccount(t, state, "bulk-partial@icloud.com", true)
	backend := &fakeMessageSyncBackend{
		imapResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}, Scanned: 12},
		webErr:     errors.New("Web 补查测试故障"),
	}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.SuccessfulAccounts != 1 || result.FailedAccounts != 0 || result.IMAPAccounts != 1 {
		t.Fatalf("IMAP 已完成时不应把批量账号计为失败：结果=%+v，错误=%v", result, err)
	}
	if !strings.Contains(result.Accounts[0].FallbackReason, "Web 补查测试故障") {
		t.Fatalf("Web 补查路径提示缺失：%+v", result.Accounts[0])
	}
}

func TestSyncExistingMailboxMessagesFallsBackToWeb(t *testing.T) {
	state := openSyncTestStore(t)
	saveSyncTestAccount(t, state, "fallback@icloud.com", true)
	backend := &fakeMessageSyncBackend{
		imapErr:   errors.New("IMAP 测试故障"),
		webResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}, Scanned: 5},
	}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.SuccessfulAccounts != 1 || result.WebAPIAccounts != 1 || result.Fallbacks != 1 {
		t.Fatalf("Web API 回退同步结果不正确：结果=%+v，错误=%v", result, err)
	}
	account := result.Accounts[0]
	if account.Method != "web_api" || !account.FallbackUsed || !strings.Contains(account.FallbackReason, "IMAP 测试故障") {
		t.Fatalf("Web API 回退详情不正确：%+v", account)
	}
	if imap, web, _ := backend.counts(); imap != 1 || web != 1 {
		t.Fatalf("回退调用次数不正确：IMAP=%d，Web=%d", imap, web)
	}
}

func TestSyncExistingMailboxMessagesUsesWebWithoutIMAP(t *testing.T) {
	state := openSyncTestStore(t)
	saveSyncTestAccount(t, state, "web-only@icloud.com", false)
	backend := &fakeMessageSyncBackend{webResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}, Scanned: 2}}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.SuccessfulAccounts != 1 || result.WebAPIAccounts != 1 || result.Fallbacks != 0 {
		t.Fatalf("纯 Web API 同步结果不正确：结果=%+v，错误=%v", result, err)
	}
	if imap, web, _ := backend.counts(); imap != 0 || web != 1 {
		t.Fatalf("纯 Web API 调用次数不正确：IMAP=%d，Web=%d", imap, web)
	}
}

func TestSyncExistingMailboxMessagesReportsBothPathFailures(t *testing.T) {
	state := openSyncTestStore(t)
	saveSyncTestAccount(t, state, "broken@icloud.com", true)
	backend := &fakeMessageSyncBackend{imapErr: errors.New("IMAP 故障"), webErr: errors.New("Web 故障")}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.FailedAccounts != 1 || len(result.Failures) != 1 {
		t.Fatalf("双路径失败统计不正确：结果=%+v，错误=%v", result, err)
	}
	message := result.Failures[0].Error
	if !strings.Contains(message, "IMAP 故障") || !strings.Contains(message, "Web 故障") {
		t.Fatalf("双路径错误信息不完整：%s", message)
	}
}

func TestSyncExistingMailboxMessagesGroupsManyAliasesByPrimaryAccount(t *testing.T) {
	state := openSyncTestStore(t)
	session, err := state.SaveICloudSession(webSyncTestSession("bulk@icloud.com"))
	if err != nil {
		t.Fatalf("创建测试 Apple 账号失败：%v", err)
	}

	const mailboxCount = 750
	for index := 0; index < mailboxCount; index++ {
		email := fmt.Sprintf("alias-%03d@icloud.com", index)
		if _, _, err := state.UpsertMailboxFromRemote(session.AccountID, domain.RemoteMailbox{Email: email, IsActive: true}, ""); err != nil {
			t.Fatalf("创建第 %d 个测试邮箱失败：%v", index+1, err)
		}
	}
	backend := &fakeMessageSyncBackend{webResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}}}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.TotalAccounts != 1 || result.TotalMailboxes != mailboxCount || result.SuccessfulAccounts != 1 {
		t.Fatalf("大量隐私邮箱分组结果不正确：结果=%+v，错误=%v", result, err)
	}
	if imap, web, _ := backend.counts(); imap != 0 || web != 1 {
		t.Fatalf("750 个别名应只产生一次账号级 Web API 调用：IMAP=%d，Web=%d", imap, web)
	}
}

func TestSyncExistingMailboxMessagesLimitsAccountConcurrency(t *testing.T) {
	state := openSyncTestStore(t)
	for index := 0; index < 5; index++ {
		saveSyncTestAccount(t, state, fmt.Sprintf("parallel-%d@icloud.com", index), false)
	}
	backend := &fakeMessageSyncBackend{delay: 30 * time.Millisecond, webResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}}}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	result, err := service.SyncExistingMailboxMessages(context.Background())
	if err != nil || result.SuccessfulAccounts != 5 {
		t.Fatalf("多账号并发同步结果不正确：结果=%+v，错误=%v", result, err)
	}
	_, web, maxActive := backend.counts()
	if web != 5 || maxActive < 2 || maxActive > 3 {
		t.Fatalf("账号级并发应在 2 到 3 之间：Web=%d，最大并发=%d", web, maxActive)
	}
}

func TestExistingMailboxMessageSyncJobPublishesAccountProgress(t *testing.T) {
	state := openSyncTestStore(t)
	for index := 0; index < 2; index++ {
		saveSyncTestAccount(t, state, fmt.Sprintf("job-%d@icloud.com", index), false)
	}
	backend := &fakeMessageSyncBackend{delay: 20 * time.Millisecond, webResult: protocol.MailSyncBatchResult{MessagesByMailbox: map[string][]protocol.ICloudSyncedMessage{}, Scanned: 4}}
	service := NewService(config.Default(), state)
	service.messageBackend = backend

	started, err := service.StartExistingMailboxMessageSync(context.Background())
	if err != nil || !started.Running || started.TotalAccounts != 2 || started.Queued != 2 {
		t.Fatalf("后台邮件同步任务启动失败：任务=%+v，错误=%v", started, err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		job := service.ExistingMailboxMessageSyncStatus()
		if !job.Running {
			if job.Status != "completed" || job.CompletedAccounts != 2 || job.SuccessfulAccounts != 2 || job.FailedAccounts != 0 || job.WebAPIAccounts != 2 || job.Scanned != 8 {
				t.Fatalf("后台邮件同步任务结果不正确：%+v", job)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("等待后台邮件同步任务超时：%+v", job)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSummarizeExistingMailboxMessageSyncErrorRemovesLongResponse(t *testing.T) {
	message := `iCloud 邮件 /mailws2/v1/thread/search HTTP 400：{"status":400,"message":"Validation failed for argument at index 0"}`
	if summary := summarizeExistingMailboxMessageSyncError(message); summary != "iCloud Web 补查请求参数被拒绝（HTTP 400）" {
		t.Fatalf("邮件同步错误摘要不正确：%s", summary)
	}
}

func openSyncTestStore(t *testing.T) *store.Store {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试数据库失败：%v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	return state
}

func saveSyncTestAccount(t *testing.T, state *store.Store, appleID string, withIMAP bool) domain.Mailbox {
	t.Helper()
	session := webSyncTestSession(appleID)
	if withIMAP {
		session.LoginStates = append(session.LoginStates, domain.LoginState{
			Kind: domain.LoginStateICloudIMAP, IMAPEmail: appleID, IMAPUsername: appleID,
			IMAPHost: "imap.mail.me.com", IMAPPort: 993, IMAPAppPassword: "test-app-password",
		})
	}
	saved, err := state.SaveICloudSession(session)
	if err != nil {
		t.Fatalf("创建测试 Apple 账号失败：%v", err)
	}
	mailbox, _, err := state.UpsertMailboxFromRemote(saved.AccountID, domain.RemoteMailbox{Email: "alias-" + strings.Split(appleID, "@")[0] + "@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatalf("创建测试隐私邮箱失败：%v", err)
	}
	return mailbox
}

func webSyncTestSession(appleID string) domain.ICloudSession {
	return domain.ICloudSession{
		AppleID: appleID, DSID: "dsid-" + appleID, MailGatewayBaseURL: "https://mail.example.test",
		Cookies: []domain.SessionCookie{{Name: "X-APPLE-WEBAUTH-TOKEN", Value: "test-token", Domain: ".icloud.com", Path: "/"}},
	}
}
