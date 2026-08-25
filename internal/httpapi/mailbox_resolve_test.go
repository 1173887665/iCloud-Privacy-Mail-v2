package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/store"
)

func TestHandleMailboxResolvePreservesInputOrderAndReportsMissing(t *testing.T) {
	database, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建临时数据库失败：%v", err)
	}
	defer database.Close()

	for _, email := range []string{"first@icloud.com", "second@icloud.com"} {
		if _, _, err := database.UpsertMailboxFromRemote("account-1", domain.RemoteMailbox{Email: email, IsActive: true}, "批量解析测试"); err != nil {
			t.Fatalf("创建测试邮箱失败：%v", err)
		}
	}

	server := &Server{store: database}
	request := httptest.NewRequest(http.MethodPost, "/api/mailboxes/resolve", strings.NewReader(`{"emails":["SECOND@icloud.com","missing@icloud.com","first@icloud.com","second@icloud.com"]}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleMailboxResolve(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("解析邮箱接口状态不正确：%d，响应=%s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Data struct {
			Items []struct {
				ID        string `json:"id"`
				Email     string `json:"email"`
				AccountID string `json:"account_id"`
			} `json:"items"`
			Missing []string `json:"missing"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析接口响应失败：%v", err)
	}
	if len(payload.Data.Items) != 2 || payload.Data.Items[0].Email != "second@icloud.com" || payload.Data.Items[1].Email != "first@icloud.com" {
		t.Fatalf("邮箱解析结果未保持输入顺序或去重失败：%+v", payload.Data.Items)
	}
	if payload.Data.Items[0].AccountID != "account-1" || payload.Data.Items[1].AccountID != "account-1" {
		t.Fatalf("邮箱解析结果缺少 Apple 账号：%+v", payload.Data.Items)
	}
	if len(payload.Data.Missing) != 1 || payload.Data.Missing[0] != "missing@icloud.com" {
		t.Fatalf("未找到邮箱结果不正确：%v", payload.Data.Missing)
	}
}
