package mailbox

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/protocol"
	"icloud-privacy-mail-v2/internal/store"
)

type Service struct {
	cfg               config.Config
	store             *store.Store
	client            *protocol.ICloudClient
	deleteClient      remoteMailboxDeleteClient
	createMu          sync.Mutex
	syncMu            sync.Mutex
	syncs             map[string]*syncCall
	cleanupMu         sync.Mutex
	cleanupState      AppleMailCleanupJob
	cleanupMailboxes  map[string]int
	cleanupCancel     context.CancelFunc
	cleanupGeneration uint64
}

type remoteMailboxDeleteClient interface {
	ListPrivacyMailboxes(context.Context, protocol.ICloudSession) ([]protocol.ICloudRemoteMailbox, error)
	DeletePrivacyMailbox(context.Context, protocol.ICloudSession, string) error
	MoveRemoteMessagesToTrash(context.Context, protocol.ICloudSession, []string) (protocol.ICloudMailCleanupResult, error)
	EmptyTrash(context.Context, protocol.ICloudSession) (int, error)
}

const mailboxCodeFreshWindow = 5 * time.Minute

type syncCall struct {
	done  chan struct{}
	count int
	err   error
}

type CodeResult struct {
	Email      string    `json:"email"`
	Code       string    `json:"code"`
	Subject    string    `json:"subject"`
	From       string    `json:"from"`
	ReceivedAt time.Time `json:"received_at"`
	MessageID  string    `json:"message_id"`
}

type MessageContentBackfillResult struct {
	Total   int
	Updated int
	Failed  int
}

type CodeQuery struct {
	After         time.Time
	Keyword       string
	SkipMessageID string
	IncludeServed bool
	MarkAsServed  bool
}

type RemoteCleanupOptions struct {
	AccountID  string `json:"account_id,omitempty"`
	MoveSynced bool   `json:"move_synced"`
	EmptyTrash bool   `json:"empty_trash"`
	PurgeLocal bool   `json:"purge_local,omitempty"`
}

type RemoteCleanupFailure struct {
	MailboxID string `json:"mailbox_id"`
	Email     string `json:"email"`
	Error     string `json:"error"`
}

type RemoteCleanupBatchResult struct {
	Cleanup         protocol.ICloudMailCleanupResult `json:"cleanup"`
	Mailboxes       int                              `json:"mailboxes"`
	FailedMailboxes int                              `json:"failed_mailboxes"`
	Failures        []RemoteCleanupFailure           `json:"failures,omitempty"`
}

type AppleMailCleanupRequest struct {
	AccountIDs []string `json:"account_ids,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Strategy   string   `json:"strategy,omitempty"`
	PurgeLocal bool     `json:"purge_local"`
}

type AppleMailCleanupFailure struct {
	AccountID string `json:"account_id"`
	AppleID   string `json:"apple_id,omitempty"`
	Error     string `json:"error"`
}

type AppleMailCleanupJob struct {
	ID                  string                    `json:"id,omitempty"`
	Running             bool                      `json:"running"`
	Status              string                    `json:"status"`
	Stage               string                    `json:"stage,omitempty"`
	AccountIDs          []string                  `json:"account_ids,omitempty"`
	TotalAccounts       int                       `json:"total_accounts"`
	TotalMailboxes      int                       `json:"total_mailboxes"`
	CompletedMailboxes  int                       `json:"completed_mailboxes"`
	SuccessfulMailboxes int                       `json:"successful_mailboxes"`
	FailedMailboxes     int                       `json:"failed_mailboxes"`
	Queued              int                       `json:"queued"`
	Active              int                       `json:"active"`
	Completed           int                       `json:"completed"`
	Success             int                       `json:"success"`
	Failed              int                       `json:"failed"`
	CurrentAccountID    string                    `json:"current_account_id,omitempty"`
	CurrentAppleID      string                    `json:"current_apple_id,omitempty"`
	CurrentFolder       string                    `json:"current_folder,omitempty"`
	FoldersScanned      int                       `json:"folders_scanned"`
	Discovered          int                       `json:"discovered"`
	MovedToTrash        int                       `json:"moved_to_trash"`
	Destroyed           int                       `json:"destroyed"`
	LocalRemoved        int                       `json:"local_removed"`
	LastError           string                    `json:"last_error,omitempty"`
	Failures            []AppleMailCleanupFailure `json:"failures,omitempty"`
	StartedAt           time.Time                 `json:"started_at,omitempty"`
	UpdatedAt           time.Time                 `json:"updated_at,omitempty"`
	CompletedAt         time.Time                 `json:"completed_at,omitempty"`
}

const appleMailCleanupStateID = "apple-mail-cleanup"

func NewService(cfg config.Config, state *store.Store) *Service {
	client := protocol.NewICloudClient()
	service := &Service{cfg: cfg, store: state, client: client, deleteClient: client, syncs: make(map[string]*syncCall), cleanupState: AppleMailCleanupJob{Status: "idle"}, cleanupMailboxes: make(map[string]int)}
	if state != nil {
		var persisted AppleMailCleanupJob
		if found, err := state.LoadRuntimeState(appleMailCleanupStateID, &persisted); err == nil && found {
			service.cleanupState = persisted
			if service.cleanupState.Running {
				service.cleanupState.Running = false
				service.cleanupState.Status = "interrupted"
				service.cleanupState.Stage = "interrupted"
				service.cleanupState.Active = 0
				service.cleanupState.LastError = "服务重启，未完成的邮件清理任务已停止，请确认云端状态后重新执行"
				service.cleanupState.UpdatedAt = time.Now()
				service.cleanupState.CompletedAt = time.Now()
				_ = state.SaveRuntimeState(appleMailCleanupStateID, service.cleanupState, true)
			}
		}
	}
	return service
}

// ImportLocal 把已有隐私邮箱绑定到 Apple 账号，不调用 Apple 创建接口。
func (s *Service) ImportLocal(accountID, email, label, note string) (domain.Mailbox, bool, error) {
	accountID = strings.TrimSpace(accountID)
	if _, ok := s.store.FindAppleAccount(accountID); !ok {
		return domain.Mailbox{}, false, errors.New("Apple 账号不存在")
	}
	email = strings.ToLower(strings.TrimSpace(email))
	address, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(strings.TrimSpace(address.Address), email) {
		return domain.Mailbox{}, false, errors.New("邮箱地址格式不正确")
	}
	mailbox, created, err := s.store.UpsertMailboxFromRemote(accountID, domain.RemoteMailbox{
		Email: email, Label: strings.TrimSpace(label), IsActive: true, Origin: "manual",
	}, firstNonEmpty(note, "手动导入本地邮箱"))
	return mailbox, created, err
}

func (s *Service) Create(ctx context.Context, accountID, label, note, channel string) (domain.Mailbox, error) {
	session, ok := s.store.ICloudSessionByAccountID(accountID)
	if !ok {
		return domain.Mailbox{}, errors.New("Apple 账号登录态不存在")
	}
	s.createMu.Lock()
	defer s.createMu.Unlock()
	label = s.store.NextMailboxLabel(label)
	channel = strings.ToLower(strings.TrimSpace(channel))
	if channel == "" {
		channel = "auto"
	}
	var remote protocol.ICloudRemoteMailbox
	var err error
	if channel == "auto" || channel == "apple_account" {
		if _, saved := protocol.LoginStateForKind(session, domain.LoginStateAppleAccount); saved {
			var updated domain.ICloudSession
			remote, updated, err = s.client.CreatePrivacyMailboxWithAppleAccount(ctx, session, s.cfg.AppleAccountAPIKey, label, note)
			if err == nil {
				_, _ = s.store.SaveICloudSession(updated)
			}
		} else if channel == "apple_account" {
			err = errors.New("该账号没有 Apple Account 新接口登录态")
		}
	}
	if remote.Email == "" && (channel == "auto" || channel == "icloud_web") {
		remote, err = s.client.CreatePrivacyMailbox(ctx, session, label, note)
	}
	if err != nil {
		return domain.Mailbox{}, err
	}
	mailbox, _, err := s.store.UpsertMailboxFromRemote(accountID, remoteMailbox(remote), note)
	return mailbox, err
}

func (s *Service) SyncRemote(ctx context.Context, accountID string) ([]domain.Mailbox, error) {
	session, ok := s.store.ICloudSessionByAccountID(accountID)
	if !ok {
		return nil, errors.New("Apple 账号登录态不存在")
	}
	remotes, err := s.client.ListPrivacyMailboxes(ctx, session)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Mailbox, 0, len(remotes))
	for _, remote := range remotes {
		mailbox, _, saveErr := s.store.UpsertMailboxFromRemote(accountID, remoteMailbox(remote), "从 Apple 服务器同步")
		if saveErr != nil {
			return out, saveErr
		}
		out = append(out, mailbox)
	}
	return out, nil
}

// DeleteCompletely 使用统一流程清理已同步的远端邮件、删除 Apple 隐私邮箱并清理本地数据。
func (s *Service) DeleteCompletely(ctx context.Context, mailboxID string) error {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return errors.New("邮箱不存在")
	}
	session, ok := s.store.ICloudSessionByAccountID(mailbox.AccountID)
	if !ok {
		return errors.New("对应 Apple 账号登录态不存在")
	}
	client := s.deleteClient
	if client == nil {
		client = s.client
	}
	if _, err := s.cleanRemoteMessages(ctx, client, mailbox, session, RemoteCleanupOptions{
		MoveSynced: true,
		EmptyTrash: true,
		PurgeLocal: true,
	}); err != nil {
		return fmt.Errorf("删除隐私邮箱前清理已同步的 Apple 远端邮件失败：%w", err)
	}
	remotes, err := client.ListPrivacyMailboxes(ctx, session)
	if err != nil {
		return err
	}
	remote, exists := matchRemote(remotes, mailbox.AnonymousID, mailbox.Email)
	if exists {
		if err := client.DeletePrivacyMailbox(ctx, session, remote.AnonymousID); err != nil {
			return err
		}
	}
	confirmed, err := client.ListPrivacyMailboxes(ctx, session)
	if err != nil {
		return fmt.Errorf("Apple 删除后确认失败：%w", err)
	}
	if _, stillExists := matchRemote(confirmed, firstNonEmpty(remote.AnonymousID, mailbox.AnonymousID), mailbox.Email); stillExists {
		return errors.New("Apple 服务器仍然存在该隐私邮箱，本地记录已保留")
	}
	return s.store.DeleteMailbox(mailboxID)
}

func (s *Service) DeleteLocal(mailboxID string) error {
	if _, err := s.store.DeleteMailboxMessages(mailboxID); err != nil {
		return fmt.Errorf("删除邮箱前清理本地邮件失败：%w", err)
	}
	return s.store.DeleteMailbox(mailboxID)
}

func (s *Service) CleanRemoteMessages(ctx context.Context, mailboxID string, options RemoteCleanupOptions) (protocol.ICloudMailCleanupResult, error) {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return protocol.ICloudMailCleanupResult{}, errors.New("邮箱不存在")
	}
	session, ok := s.store.ICloudSessionByAccountID(mailbox.AccountID)
	if !ok {
		return protocol.ICloudMailCleanupResult{}, errors.New("对应 Apple 账号登录态不存在")
	}
	options = normalizeRemoteCleanupOptions(options)
	client := s.deleteClient
	if client == nil {
		client = s.client
	}
	return s.cleanRemoteMessages(ctx, client, mailbox, session, options)
}

// cleanRemoteMessages 是详情清理和彻底删除共同使用的已同步邮件清理实现。
func (s *Service) cleanRemoteMessages(ctx context.Context, client remoteMailboxDeleteClient, mailbox domain.Mailbox, session protocol.ICloudSession, options RemoteCleanupOptions) (protocol.ICloudMailCleanupResult, error) {
	result := protocol.ICloudMailCleanupResult{}
	if options.MoveSynced {
		moved, err := client.MoveRemoteMessagesToTrash(ctx, session, remoteMessageIDs(s.store.MessagesForMailbox(mailbox.ID)))
		result.MovedToTrash += moved.MovedToTrash
		result.Skipped += moved.Skipped
		if err != nil {
			return result, err
		}
		result.MovedRemoteIDs = append(result.MovedRemoteIDs, moved.MovedRemoteIDs...)
		result.AbsentRemoteIDs = append(result.AbsentRemoteIDs, moved.AbsentRemoteIDs...)
	}
	if options.EmptyTrash {
		destroyed, err := client.EmptyTrash(ctx, session)
		result.Destroyed += destroyed
		if err != nil {
			return result, err
		}
	}
	if options.PurgeLocal {
		removed, err := s.store.DeleteMailboxMessages(mailbox.ID)
		if err != nil {
			return result, err
		}
		result.LocalRemoved += removed
	} else if options.MoveSynced {
		localIDs := append(append([]string(nil), result.MovedRemoteIDs...), result.AbsentRemoteIDs...)
		removed, err := s.store.DeleteMailboxMessagesByRemoteIDs(mailbox.ID, localIDs)
		if err != nil {
			return result, err
		}
		result.LocalRemoved += removed
	}
	return result, nil
}

func (s *Service) CleanRemoteMailboxes(ctx context.Context, options RemoteCleanupOptions) RemoteCleanupBatchResult {
	options = normalizeRemoteCleanupOptions(options)
	options.AccountID = strings.TrimSpace(options.AccountID)
	client := s.deleteClient
	if client == nil {
		client = s.client
	}
	result := RemoteCleanupBatchResult{}
	cleanedTrash := make(map[string]bool)
	for _, mailbox := range s.store.AllMailboxes() {
		if ctx.Err() != nil {
			break
		}
		if options.AccountID != "" && mailbox.AccountID != options.AccountID {
			continue
		}
		if !mailbox.ICloudActive || mailbox.Status == domain.StatusDisabled {
			result.Cleanup.Skipped++
			if options.PurgeLocal {
				removed, err := s.store.DeleteMailboxMessages(mailbox.ID)
				if err != nil {
					result.FailedMailboxes++
					result.Failures = append(result.Failures, RemoteCleanupFailure{MailboxID: mailbox.ID, Email: mailbox.Email, Error: err.Error()})
				} else {
					result.Cleanup.LocalRemoved += removed
				}
			}
			continue
		}
		session, ok := s.store.ICloudSessionByAccountID(mailbox.AccountID)
		if !ok {
			result.Cleanup.Skipped++
			if options.PurgeLocal {
				removed, err := s.store.DeleteMailboxMessages(mailbox.ID)
				if err != nil {
					result.FailedMailboxes++
					result.Failures = append(result.Failures, RemoteCleanupFailure{MailboxID: mailbox.ID, Email: mailbox.Email, Error: err.Error()})
				} else {
					result.Cleanup.LocalRemoved += removed
				}
			}
			continue
		}
		if options.MoveSynced {
			moved, err := client.MoveRemoteMessagesToTrash(ctx, session, remoteMessageIDs(s.store.MessagesForMailbox(mailbox.ID)))
			result.Cleanup.MovedToTrash += moved.MovedToTrash
			result.Cleanup.Skipped += moved.Skipped
			if err != nil {
				result.FailedMailboxes++
				result.Failures = append(result.Failures, RemoteCleanupFailure{MailboxID: mailbox.ID, Email: mailbox.Email, Error: err.Error()})
				if options.PurgeLocal {
					removed, localErr := s.store.DeleteMailboxMessages(mailbox.ID)
					if localErr == nil {
						result.Cleanup.LocalRemoved += removed
					}
				}
				continue
			}
			var removed int
			if options.PurgeLocal {
				removed, err = s.store.DeleteMailboxMessages(mailbox.ID)
			} else {
				localIDs := append(append([]string(nil), moved.MovedRemoteIDs...), moved.AbsentRemoteIDs...)
				removed, err = s.store.DeleteMailboxMessagesByRemoteIDs(mailbox.ID, localIDs)
			}
			if err != nil {
				result.FailedMailboxes++
				result.Failures = append(result.Failures, RemoteCleanupFailure{MailboxID: mailbox.ID, Email: mailbox.Email, Error: err.Error()})
				continue
			}
			result.Cleanup.LocalRemoved += removed
		} else if options.PurgeLocal {
			removed, err := s.store.DeleteMailboxMessages(mailbox.ID)
			if err != nil {
				result.FailedMailboxes++
				result.Failures = append(result.Failures, RemoteCleanupFailure{MailboxID: mailbox.ID, Email: mailbox.Email, Error: err.Error()})
				continue
			}
			result.Cleanup.LocalRemoved += removed
		}
		result.Mailboxes++
		sessionKey := firstNonEmpty(session.AccountID, session.DSID, session.AppleID, mailbox.AccountID)
		if options.EmptyTrash && !cleanedTrash[sessionKey] {
			destroyed, err := client.EmptyTrash(ctx, session)
			result.Cleanup.Destroyed += destroyed
			if err != nil {
				result.FailedMailboxes++
				result.Failures = append(result.Failures, RemoteCleanupFailure{MailboxID: mailbox.ID, Email: mailbox.Email, Error: err.Error()})
				continue
			}
			cleanedTrash[sessionKey] = true
		}
	}
	return result
}

func (s *Service) StartAppleMailCleanup(parent context.Context, request AppleMailCleanupRequest) (AppleMailCleanupJob, error) {
	request.Scope = strings.ToLower(strings.TrimSpace(request.Scope))
	if request.Scope == "" {
		request.Scope = "all"
	}
	if request.Scope != "all" {
		return AppleMailCleanupJob{}, errors.New("当前只支持清理全部 Apple 云端邮件")
	}
	request.Strategy = strings.ToLower(strings.TrimSpace(request.Strategy))
	if request.Strategy == "" {
		request.Strategy = "move_then_destroy"
	}
	if request.Strategy != "move_then_destroy" {
		return AppleMailCleanupJob{}, errors.New("当前只支持先移入废纸篓再彻底删除")
	}
	accountIDs, err := s.cleanupAccountIDs(request.AccountIDs)
	if err != nil {
		return AppleMailCleanupJob{}, err
	}
	mailboxCounts, totalMailboxes := s.cleanupMailboxCounts(accountIDs)
	if parent == nil {
		parent = context.Background()
	}

	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if s.cleanupState.Running {
		return AppleMailCleanupJob{}, errors.New("全部 Apple 邮件清理任务正在运行")
	}
	ctx, cancel := context.WithCancel(parent)
	s.cleanupCancel = cancel
	s.cleanupMailboxes = mailboxCounts
	s.cleanupGeneration++
	generation := s.cleanupGeneration
	now := time.Now()
	s.cleanupState = AppleMailCleanupJob{
		ID:             fmt.Sprintf("apple_mail_cleanup_%d", now.UnixNano()),
		Running:        true,
		Status:         "queued",
		Stage:          "queued",
		AccountIDs:     append([]string(nil), accountIDs...),
		TotalAccounts:  len(accountIDs),
		TotalMailboxes: totalMailboxes,
		Queued:         len(accountIDs),
		Failures:       []AppleMailCleanupFailure{},
		StartedAt:      now,
		UpdatedAt:      now,
	}
	s.publishAppleMailCleanupLocked()
	out := s.appleMailCleanupSnapshotLocked()
	go s.runAppleMailCleanup(ctx, request, accountIDs, generation)
	return out, nil
}

func (s *Service) AppleMailCleanupStatus() AppleMailCleanupJob {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	return s.appleMailCleanupSnapshotLocked()
}

func (s *Service) CancelAppleMailCleanup(message string) AppleMailCleanupJob {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if !s.cleanupState.Running {
		return s.appleMailCleanupSnapshotLocked()
	}
	if s.cleanupCancel != nil {
		s.cleanupCancel()
		s.cleanupCancel = nil
	}
	s.cleanupGeneration++
	now := time.Now()
	s.cleanupState.Running = false
	s.cleanupState.Status = "cancelled"
	s.cleanupState.Stage = "cancelled"
	s.cleanupState.Active = 0
	s.cleanupState.Queued = 0
	s.cleanupState.CurrentFolder = ""
	s.cleanupState.UpdatedAt = now
	s.cleanupState.CompletedAt = now
	if strings.TrimSpace(message) == "" {
		message = "全部 Apple 邮件清理任务已取消"
	}
	s.cleanupState.LastError = strings.TrimSpace(message)
	s.publishAppleMailCleanupLocked()
	return s.appleMailCleanupSnapshotLocked()
}

func (s *Service) runAppleMailCleanup(ctx context.Context, request AppleMailCleanupRequest, accountIDs []string, generation uint64) {
	for index, accountID := range accountIDs {
		if ctx.Err() != nil || !s.beginAppleMailCleanupAccount(generation, accountID, len(accountIDs)-index-1) {
			return
		}
		account, _ := s.store.FindAppleAccount(accountID)
		session, ok := s.store.ICloudSessionByAccountID(accountID)
		if !ok {
			s.finishAppleMailCleanupAccount(generation, accountID, account.AppleID, 0, errors.New("该账号没有可用的 iCloud Web 旧接口登录态"))
			continue
		}
		base := s.appleMailCleanupTotals(generation)
		_, cleanupErr := s.client.CleanAllRemoteMail(ctx, session, func(progress protocol.ICloudAllMailCleanupProgress) {
			s.updateAppleMailCleanupProgress(generation, base, progress)
		})
		localRemoved := 0
		if cleanupErr == nil && request.PurgeLocal {
			localRemoved, cleanupErr = s.store.DeleteAccountMessages(accountID)
			if cleanupErr != nil {
				cleanupErr = fmt.Errorf("Apple 云端邮件已清理，但本地邮件清理失败：%w", cleanupErr)
			}
		}
		if ctx.Err() != nil {
			return
		}
		s.finishAppleMailCleanupAccount(generation, accountID, account.AppleID, localRemoved, cleanupErr)
	}
	s.finishAppleMailCleanupJob(generation)
}

type appleMailCleanupTotals struct {
	FoldersScanned int
	Discovered     int
	MovedToTrash   int
	Destroyed      int
}

func (s *Service) appleMailCleanupTotals(generation uint64) appleMailCleanupTotals {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if generation != s.cleanupGeneration {
		return appleMailCleanupTotals{}
	}
	return appleMailCleanupTotals{
		FoldersScanned: s.cleanupState.FoldersScanned,
		Discovered:     s.cleanupState.Discovered,
		MovedToTrash:   s.cleanupState.MovedToTrash,
		Destroyed:      s.cleanupState.Destroyed,
	}
}

func (s *Service) beginAppleMailCleanupAccount(generation uint64, accountID string, queued int) bool {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if generation != s.cleanupGeneration || !s.cleanupState.Running {
		return false
	}
	account, _ := s.store.FindAppleAccount(accountID)
	s.cleanupState.Status = "running"
	s.cleanupState.Stage = "scanning"
	s.cleanupState.Active = 1
	s.cleanupState.Queued = queued
	s.cleanupState.CurrentAccountID = accountID
	s.cleanupState.CurrentAppleID = account.AppleID
	s.cleanupState.CurrentFolder = ""
	s.cleanupState.UpdatedAt = time.Now()
	s.publishAppleMailCleanupLocked()
	return true
}

func (s *Service) updateAppleMailCleanupProgress(generation uint64, base appleMailCleanupTotals, progress protocol.ICloudAllMailCleanupProgress) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if generation != s.cleanupGeneration || !s.cleanupState.Running {
		return
	}
	s.cleanupState.Stage = progress.Stage
	s.cleanupState.CurrentFolder = progress.Folder
	s.cleanupState.FoldersScanned = base.FoldersScanned + progress.Result.FoldersScanned
	s.cleanupState.Discovered = base.Discovered + progress.Result.Discovered
	s.cleanupState.MovedToTrash = base.MovedToTrash + progress.Result.MovedToTrash
	s.cleanupState.Destroyed = base.Destroyed + progress.Result.Destroyed
	s.cleanupState.UpdatedAt = time.Now()
	s.publishAppleMailCleanupLocked()
}

func (s *Service) finishAppleMailCleanupAccount(generation uint64, accountID, appleID string, localRemoved int, cleanupErr error) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if generation != s.cleanupGeneration || !s.cleanupState.Running {
		return
	}
	s.cleanupState.Completed++
	mailboxCount := s.cleanupMailboxes[accountID]
	s.cleanupState.CompletedMailboxes += mailboxCount
	s.cleanupState.Active = 0
	s.cleanupState.LocalRemoved += localRemoved
	s.cleanupState.CurrentFolder = ""
	s.cleanupState.UpdatedAt = time.Now()
	if cleanupErr != nil {
		s.cleanupState.Failed++
		s.cleanupState.FailedMailboxes += mailboxCount
		s.cleanupState.LastError = cleanupErr.Error()
		s.cleanupState.Failures = append(s.cleanupState.Failures, AppleMailCleanupFailure{AccountID: accountID, AppleID: appleID, Error: cleanupErr.Error()})
		s.cleanupState.Stage = "account-failed"
	} else {
		s.cleanupState.Success++
		s.cleanupState.SuccessfulMailboxes += mailboxCount
		s.cleanupState.Stage = "account-completed"
	}
	s.publishAppleMailCleanupLocked()
}

func (s *Service) finishAppleMailCleanupJob(generation uint64) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()
	if generation != s.cleanupGeneration || !s.cleanupState.Running {
		return
	}
	now := time.Now()
	s.cleanupState.Running = false
	s.cleanupState.Active = 0
	s.cleanupState.Queued = 0
	s.cleanupState.CurrentAccountID = ""
	s.cleanupState.CurrentAppleID = ""
	s.cleanupState.CurrentFolder = ""
	s.cleanupState.CompletedAt = now
	s.cleanupState.UpdatedAt = now
	s.cleanupCancel = nil
	if s.cleanupState.Failed > 0 {
		s.cleanupState.Status = "partial"
		s.cleanupState.Stage = "partial"
	} else {
		s.cleanupState.Status = "completed"
		s.cleanupState.Stage = "completed"
		s.cleanupState.LastError = ""
	}
	s.publishAppleMailCleanupLocked()
}

func (s *Service) cleanupAccountIDs(requested []string) ([]string, error) {
	seen := make(map[string]bool)
	accountIDs := make([]string, 0)
	if len(requested) == 0 {
		for _, account := range s.store.AppleAccounts() {
			if account.ID != "" && !seen[account.ID] {
				seen[account.ID] = true
				accountIDs = append(accountIDs, account.ID)
			}
		}
	} else {
		for _, accountID := range requested {
			accountID = strings.TrimSpace(accountID)
			if accountID == "" || seen[accountID] {
				continue
			}
			if _, ok := s.store.FindAppleAccount(accountID); !ok {
				return nil, fmt.Errorf("Apple 账号不存在：%s", accountID)
			}
			seen[accountID] = true
			accountIDs = append(accountIDs, accountID)
		}
	}
	if len(accountIDs) == 0 {
		return nil, errors.New("没有可清理的 Apple 账号")
	}
	return accountIDs, nil
}

func (s *Service) cleanupMailboxCounts(accountIDs []string) (map[string]int, int) {
	selected := make(map[string]bool, len(accountIDs))
	for _, accountID := range accountIDs {
		if accountID = strings.TrimSpace(accountID); accountID != "" {
			selected[accountID] = true
		}
	}
	counts := make(map[string]int, len(selected))
	total := 0
	for _, mailbox := range s.store.AllMailboxes() {
		if selected[mailbox.AccountID] {
			counts[mailbox.AccountID]++
			total++
		}
	}
	return counts, total
}

func (s *Service) publishAppleMailCleanupLocked() {
	if s.store != nil {
		_ = s.store.SaveRuntimeState(appleMailCleanupStateID, s.cleanupState, true)
	}
}

func (s *Service) appleMailCleanupSnapshotLocked() AppleMailCleanupJob {
	out := s.cleanupState
	out.AccountIDs = append([]string(nil), s.cleanupState.AccountIDs...)
	out.Failures = append([]AppleMailCleanupFailure(nil), s.cleanupState.Failures...)
	if out.TotalMailboxes == 0 && len(out.AccountIDs) > 0 {
		_, out.TotalMailboxes = s.cleanupMailboxCounts(out.AccountIDs)
	}
	return out
}

func (s *Service) SyncMessages(ctx context.Context, mailboxID string) (int, error) {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return 0, errors.New("邮箱不存在")
	}
	mailboxes := s.mailboxesForAccount(mailbox.AccountID)
	found := false
	for _, item := range mailboxes {
		if item.ID == mailbox.ID {
			found = true
			break
		}
	}
	if !found {
		mailboxes = append(mailboxes, mailbox)
	}
	return s.SyncMailboxBatch(ctx, mailboxes, time.Time{}, "", s.cfg.MailWatcherFetchLimit)
}

// SyncMailboxBatch 按 Apple 账号批量拉取一次收件箱，再把邮件分发给对应隐私邮箱。
func (s *Service) SyncMailboxBatch(ctx context.Context, mailboxes []domain.Mailbox, after time.Time, keyword string, maxMessages int) (int, error) {
	if len(mailboxes) == 0 {
		return 0, nil
	}
	if maxMessages <= 0 {
		maxMessages = s.cfg.MailWatcherFetchLimit
	}
	groups := make(map[string][]domain.Mailbox)
	order := make([]string, 0)
	for _, mailbox := range mailboxes {
		key := firstNonEmpty(mailbox.AccountID, mailbox.OwnerID, "__legacy__")
		if _, exists := groups[key]; !exists {
			order = append(order, key)
		}
		groups[key] = append(groups[key], mailbox)
	}
	total := 0
	for _, key := range order {
		count, err := s.syncGroup(ctx, key, groups[key], after, keyword, maxMessages)
		total += count
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (s *Service) syncGroup(ctx context.Context, key string, mailboxes []domain.Mailbox, after time.Time, keyword string, maxMessages int) (int, error) {
	s.syncMu.Lock()
	if running := s.syncs[key]; running != nil {
		s.syncMu.Unlock()
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-running.done:
			return running.count, running.err
		}
	}
	call := &syncCall{done: make(chan struct{})}
	s.syncs[key] = call
	s.syncMu.Unlock()

	call.count, call.err = s.syncGroupNow(ctx, mailboxes, after, keyword, maxMessages)
	s.syncMu.Lock()
	delete(s.syncs, key)
	close(call.done)
	s.syncMu.Unlock()
	return call.count, call.err
}

func (s *Service) syncGroupNow(ctx context.Context, mailboxes []domain.Mailbox, after time.Time, keyword string, maxMessages int) (int, error) {
	refreshed := make([]domain.Mailbox, 0, len(mailboxes))
	for _, mailbox := range mailboxes {
		if current, ok := s.store.FindMailboxByID(mailbox.ID); ok {
			refreshed = append(refreshed, current)
		}
	}
	if len(refreshed) == 0 {
		return 0, nil
	}
	session, ok := s.store.ICloudSessionByAccountID(refreshed[0].AccountID)
	if !ok {
		return 0, errors.New("对应 Apple 账号登录态不存在")
	}

	messagesByMailbox := make(map[string][]protocol.ICloudSyncedMessage)
	lastUID := ""
	source := "icloud"
	if imapState, saved := protocol.LoginStateForKind(session, domain.LoginStateICloudIMAP); saved {
		result, err := protocol.SyncICloudIMAPMessagesDetailed(ctx, imapState, refreshed, after, keyword, maxMessages)
		if err != nil {
			return 0, err
		}
		messagesByMailbox = result.MessagesByMailbox
		lastUID = strings.TrimSpace(result.LastUID)
		source = "imap"
		if lastUID != "" && (lastUID != strings.TrimSpace(imapState.IMAPLastSyncUID) || imapState.IMAPLastSyncAt.IsZero() || time.Since(imapState.IMAPLastSyncAt) >= time.Minute) {
			imapState.IMAPLastSyncAt = time.Now()
			imapState.IMAPLastSyncUID = lastUID
			session = protocol.WithLoginState(session, imapState)
			if _, err := s.store.SaveICloudSession(session); err != nil {
				return 0, err
			}
		}
	} else {
		var err error
		messagesByMailbox, err = s.client.SyncMailboxMessagesBatch(ctx, session, refreshed, after, keyword, maxMessages)
		if err != nil {
			return 0, err
		}
	}

	syncedAt := time.Now()
	updates := make([]store.MailboxSyncUpdate, 0, len(refreshed))
	for _, mailbox := range refreshed {
		mailboxUID := firstNonEmpty(lastUID, mailbox.LastSyncUID)
		update := store.MailboxSyncUpdate{MailboxID: mailbox.ID, LastUID: mailboxUID, SyncedAt: syncedAt}
		for _, message := range messagesByMailbox[mailbox.ID] {
			remoteID := firstNonEmpty(message.RemoteID, message.UID)
			update.Messages = append(update.Messages, store.MailboxSyncMessage{
				RemoteID: remoteID, Source: source, Subject: message.Subject, From: message.From,
				Body: message.Body, HTMLBody: message.HTMLBody, ContentType: message.ContentType, ReceivedAt: message.ReceivedAt,
			})
		}
		updates = append(updates, update)
	}
	return s.store.ApplyMailboxSyncBatch(updates)
}

// MessageContent 返回本地完整邮件；旧 IMAP 缓存缺少 HTML 时会按 UID 自动补全。
func (s *Service) MessageContent(ctx context.Context, mailboxID, messageID string) (domain.Message, error) {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return domain.Message{}, errors.New("邮箱不存在")
	}
	message, ok := s.store.FindMessageForMailbox(mailbox.ID, messageID)
	if !ok {
		return domain.Message{}, errors.New("邮件不存在")
	}
	if strings.TrimSpace(message.HTMLBody) != "" || strings.TrimSpace(message.ContentType) != "" {
		return message, nil
	}
	if !strings.EqualFold(strings.TrimSpace(message.Source), "imap") {
		return message, nil
	}
	uid := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(message.RemoteID), "imap:"))
	if uid == "" || uid == message.RemoteID {
		return message, nil
	}
	session, ok := s.store.ICloudSessionByAccountID(mailbox.AccountID)
	if !ok {
		return domain.Message{}, errors.New("对应 Apple 账号登录态不存在")
	}
	imapState, ok := protocol.LoginStateForKind(session, domain.LoginStateICloudIMAP)
	if !ok {
		return message, nil
	}
	fetched, err := protocol.FetchICloudIMAPMessageByUID(ctx, imapState, uid)
	if err != nil {
		return domain.Message{}, err
	}
	return s.store.UpdateMessageContent(mailbox.ID, message.ID, fetched.Body, fetched.HTMLBody, fetched.ContentType)
}

// BackfillMessageContent 批量补全旧 IMAP 邮件的 HTML 与纯文本正文。
func (s *Service) BackfillMessageContent(ctx context.Context) (MessageContentBackfillResult, error) {
	missing := s.store.MessagesMissingContent(0)
	result := MessageContentBackfillResult{Total: len(missing)}
	if len(missing) == 0 {
		return result, nil
	}
	mailboxes := make(map[string]domain.Mailbox)
	for _, mailbox := range s.store.AllMailboxes() {
		mailboxes[mailbox.ID] = mailbox
	}
	type target struct {
		message domain.Message
		mailbox domain.Mailbox
	}
	groups := make(map[string][]target)
	order := make([]string, 0)
	for _, message := range missing {
		mailbox, ok := mailboxes[message.MailboxID]
		if !ok || strings.TrimSpace(mailbox.AccountID) == "" {
			continue
		}
		if _, exists := groups[mailbox.AccountID]; !exists {
			order = append(order, mailbox.AccountID)
		}
		groups[mailbox.AccountID] = append(groups[mailbox.AccountID], target{message: message, mailbox: mailbox})
	}
	var failures []string
	for _, accountID := range order {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		session, ok := s.store.ICloudSessionByAccountID(accountID)
		if !ok {
			failures = append(failures, accountID+"：Apple 账号登录态不存在")
			continue
		}
		imapState, ok := protocol.LoginStateForKind(session, domain.LoginStateICloudIMAP)
		if !ok {
			failures = append(failures, accountID+"：IMAP 取码登录不存在")
			continue
		}
		uidTargets := make(map[string][]target)
		uidOrder := make([]string, 0)
		for _, item := range groups[accountID] {
			uid := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item.message.RemoteID), "imap:"))
			if uid == "" || uid == item.message.RemoteID {
				continue
			}
			if _, exists := uidTargets[uid]; !exists {
				uidOrder = append(uidOrder, uid)
			}
			uidTargets[uid] = append(uidTargets[uid], item)
		}
		fetched, fetchErr := protocol.FetchICloudIMAPMessagesByUID(ctx, imapState, uidOrder)
		updates := make([]store.MessageContentUpdate, 0, len(groups[accountID]))
		for uid, items := range uidTargets {
			message, ok := fetched[uid]
			if !ok {
				continue
			}
			for _, item := range items {
				updates = append(updates, store.MessageContentUpdate{MailboxID: item.mailbox.ID, MessageID: item.message.ID, Body: message.Body, HTMLBody: message.HTMLBody, ContentType: message.ContentType})
			}
		}
		updated, updateErr := s.store.ApplyMessageContentUpdates(updates)
		result.Updated += updated
		if fetchErr != nil {
			failures = append(failures, accountID+"："+fetchErr.Error())
		}
		if updateErr != nil {
			failures = append(failures, accountID+"："+updateErr.Error())
		}
	}
	result.Failed = len(s.store.MessagesMissingContent(0))
	if result.Failed > result.Total {
		result.Failed = result.Total
	}
	result.Updated = result.Total - result.Failed
	if len(failures) > 0 {
		return result, errors.New(strings.Join(failures, "；"))
	}
	return result, nil
}

func (s *Service) Code(ctx context.Context, mailboxID string, after time.Time, keyword string, allowStale bool) (CodeResult, error) {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return CodeResult{}, errors.New("邮箱不存在")
	}
	query := CodeQuery{After: after, Keyword: keyword, SkipMessageID: mailbox.LastCodeMessageID, MarkAsServed: true}
	if result, found, err := s.findCode(mailbox, query); found || err != nil {
		return result, err
	}
	if _, err := s.SyncMessages(ctx, mailboxID); err != nil && !allowStale {
		return CodeResult{}, err
	}
	mailbox, _ = s.store.FindMailboxByID(mailboxID)
	if result, found, err := s.findCode(mailbox, query); found || err != nil {
		return result, err
	}
	return CodeResult{}, errors.New("暂未收到验证码")
}

// CachedCode 只读取本地邮件缓存，不触发 Apple 或 IMAP 网络请求。
func (s *Service) CachedCode(mailboxID string, after time.Time, keyword string, allowStale bool) (CodeResult, bool, error) {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return CodeResult{}, false, errors.New("邮箱不存在")
	}
	query := CodeQuery{After: after, Keyword: keyword, SkipMessageID: mailbox.LastCodeMessageID, MarkAsServed: true}
	return s.findCode(mailbox, query)
}

// CachedCodeWithQuery 支持预览、缓存读取和固定请求起点，便于合并并发取码请求。
func (s *Service) CachedCodeWithQuery(mailboxID string, query CodeQuery) (CodeResult, bool, error) {
	mailbox, ok := s.store.FindMailboxByID(mailboxID)
	if !ok {
		return CodeResult{}, false, errors.New("邮箱不存在")
	}
	return s.findCode(mailbox, query)
}

func (s *Service) findCode(mailbox domain.Mailbox, query CodeQuery) (CodeResult, bool, error) {
	keyword := strings.TrimSpace(query.Keyword)
	if keyword == "" {
		keyword = "OpenAI"
	}
	skipMessageID := strings.TrimSpace(query.SkipMessageID)
	if query.IncludeServed {
		skipMessageID = ""
	}
	after := query.After
	if skipMessageID != "" {
		if servedMessage, ok := s.store.FindMessageForMailbox(mailbox.ID, skipMessageID); ok {
			servedAt := servedMessage.ReceivedAt
			if servedAt.IsZero() {
				servedAt = servedMessage.CreatedAt
			}
			if !servedAt.IsZero() && !servedAt.Before(after) {
				after = servedAt.Add(time.Nanosecond)
			}
		}
	}
	after = codeAfter(after, time.Now())
	for _, message := range s.store.MessagesForMailbox(mailbox.ID) {
		messageTime := message.ReceivedAt
		if messageTime.IsZero() {
			messageTime = message.CreatedAt
		}
		if messageTime.IsZero() || messageTime.Before(after) {
			continue
		}
		text := message.Subject + " " + message.From + " " + message.Body
		if !strings.EqualFold(keyword, "OpenAI") && !strings.Contains(strings.ToLower(text), strings.ToLower(keyword)) {
			continue
		}
		if skipMessageID != "" && message.ID == skipMessageID {
			continue
		}
		code := protocol.ExtractOTP(message.Subject + "\n" + message.Body)
		if code == "" {
			continue
		}
		if query.MarkAsServed {
			if err := s.store.SetMailboxLastCode(mailbox.ID, message.ID, time.Now()); err != nil {
				return CodeResult{}, false, err
			}
		}
		return CodeResult{Email: mailbox.Email, Code: code, Subject: message.Subject, From: message.From, ReceivedAt: messageTime, MessageID: message.ID}, true, nil
	}
	return CodeResult{}, false, nil
}

func codeAfter(after, now time.Time) time.Time {
	cutoff := now.Add(-mailboxCodeFreshWindow)
	if after.After(cutoff) {
		return after
	}
	return cutoff
}

func (s *Service) mailboxesForAccount(accountID string) []domain.Mailbox {
	accountID = strings.TrimSpace(accountID)
	out := make([]domain.Mailbox, 0)
	for _, mailbox := range s.store.AllMailboxes() {
		if mailbox.AccountID != accountID || !mailbox.ICloudActive || mailbox.Status == domain.StatusDisabled {
			continue
		}
		out = append(out, mailbox)
	}
	return out
}

func normalizeRemoteCleanupOptions(options RemoteCleanupOptions) RemoteCleanupOptions {
	if !options.MoveSynced && !options.EmptyTrash {
		options.MoveSynced = true
		options.EmptyTrash = true
	}
	return options
}

func remoteMessageIDs(messages []domain.Message) []string {
	out := make([]string, 0, len(messages))
	seen := make(map[string]bool)
	for _, message := range messages {
		remoteID := strings.TrimSpace(message.RemoteID)
		if remoteID == "" || seen[remoteID] {
			continue
		}
		seen[remoteID] = true
		out = append(out, remoteID)
	}
	return out
}

func remoteMailbox(remote protocol.ICloudRemoteMailbox) domain.RemoteMailbox {
	return domain.RemoteMailbox{AnonymousID: remote.AnonymousID, Email: remote.Email, Label: remote.Label, Note: remote.Note, IsActive: remote.IsActive, Origin: remote.Origin}
}

func matchRemote(remotes []protocol.ICloudRemoteMailbox, anonymousID, email string) (protocol.ICloudRemoteMailbox, bool) {
	for _, remote := range remotes {
		if strings.TrimSpace(anonymousID) != "" && remote.AnonymousID == strings.TrimSpace(anonymousID) {
			return remote, true
		}
		if strings.EqualFold(remote.Email, strings.TrimSpace(email)) {
			return remote, true
		}
	}
	return protocol.ICloudRemoteMailbox{}, false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
