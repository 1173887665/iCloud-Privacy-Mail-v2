package apple

import (
	"path/filepath"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/store"
)

func TestAccountReturnsIMAPCredentialsOnlyInDetail(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建临时数据库失败：%v", err)
	}
	defer database.Close()

	const appPassword = "abcd-efgh-ijkl-mnop"
	session, err := database.SaveICloudSession(domain.ICloudSession{
		AppleID: "imap@icloud.com",
		LoginStates: []domain.LoginState{{
			Kind:            domain.LoginStateICloudIMAP,
			SavedAt:         time.Now(),
			IMAPEmail:       "mail@icloud.com",
			IMAPAppPassword: appPassword,
		}},
	})
	if err != nil {
		t.Fatalf("保存 IMAP 登录态失败：%v", err)
	}

	service := NewService(config.Config{}, database)
	items := service.Accounts()
	if len(items) != 1 {
		t.Fatalf("账号列表数量不正确：%d", len(items))
	}
	if items[0].IMAPEmail != "" || items[0].IMAPAppPassword != "" {
		t.Fatalf("账号列表不应返回 IMAP 凭据：%+v", items[0])
	}

	detail, err := service.Account(session.AccountID)
	if err != nil {
		t.Fatalf("读取账号详情失败：%v", err)
	}
	if detail.IMAPEmail != "mail@icloud.com" || detail.IMAPAppPassword != appPassword {
		t.Fatalf("账号详情中的 IMAP 凭据不正确：%+v", detail)
	}
}
