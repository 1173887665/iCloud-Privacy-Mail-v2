package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/store"
)

func TestPublicCodePageListsAndReadsMailboxMessages(t *testing.T) {
	server, state, mailbox := newPublicCodeTestServer(t, true)
	now := time.Now().UTC().Add(-10 * time.Minute).Truncate(time.Second)
	created, err := state.ApplyMailboxSyncBatch([]store.MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []store.MailboxSyncMessage{{
		RemoteID: "imap:42", Source: "imap", Subject: "登录验证码", From: "OpenAI <noreply@example.com>",
		Body: "验证码是 123456", HTMLBody: "<p>验证码是 <strong>123456</strong></p>", ContentType: "text/html", ReceivedAt: now,
	}}}})
	if err != nil || created != 1 {
		t.Fatalf("准备测试邮件失败：created=%d err=%v", created, err)
	}

	listPath := "/api/v1/public-code/messages?email=" + url.QueryEscape(mailbox.Email)
	list := publicCodeTestRequest(t, server, listPath)
	if list.Code != http.StatusOK {
		t.Fatalf("邮件列表接口状态码为 %d：%s", list.Code, list.Body.String())
	}
	data := publicCodeTestData(t, list)
	items, ok := data["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("邮件列表不正确：%+v", data)
	}
	summary, _ := items[0].(map[string]any)
	if summary["subject"] != "登录验证码" || summary["body"] != nil || summary["html_body"] != nil {
		t.Fatalf("邮件摘要字段不正确：%+v", summary)
	}

	messageID, _ := summary["id"].(string)
	detailPath := "/api/v1/public-code/messages/" + url.PathEscape(messageID) + "?email=" + url.QueryEscape(mailbox.Email)
	detail := publicCodeTestRequest(t, server, detailPath)
	if detail.Code != http.StatusOK {
		t.Fatalf("邮件详情接口状态码为 %d：%s", detail.Code, detail.Body.String())
	}
	message, _ := publicCodeTestData(t, detail)["message"].(map[string]any)
	if message["body"] != "验证码是 123456" || message["html_body"] == "" || message["mailbox_id"] != nil || message["remote_id"] != nil {
		t.Fatalf("邮件详情字段不正确：%+v", message)
	}
}

func TestPublicCodePageMessagesRequireEnabledSetting(t *testing.T) {
	server, _, mailbox := newPublicCodeTestServer(t, false)
	response := publicCodeTestRequest(t, server, "/api/v1/public-code/messages?email="+url.QueryEscape(mailbox.Email))
	if response.Code != http.StatusForbidden {
		t.Fatalf("关闭公共页面后的状态码为 %d，期望 403：%s", response.Code, response.Body.String())
	}
}

func TestPublicMailboxCodeReturnsLatestCodeRepeatedly(t *testing.T) {
	server, state, mailbox := newPublicCodeTestServer(t, true)
	settings := state.Settings()
	settings.EnablePublicMailboxAPI = true
	if _, err := state.SaveSettings(settings); err != nil {
		t.Fatalf("开启公共邮箱 API 失败：%v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := state.ApplyMailboxSyncBatch([]store.MailboxSyncUpdate{{MailboxID: mailbox.ID, Messages: []store.MailboxSyncMessage{{
		RemoteID: "imap:latest", Source: "imap", Subject: "OpenAI 验证码 654321", Body: "验证码 654321", ReceivedAt: now,
	}}}}); err != nil {
		t.Fatalf("准备验证码邮件失败：%v", err)
	}
	path := "/api/v1/mailboxes/" + url.PathEscape(mailbox.Email) + "/code?key=" + url.QueryEscape(mailbox.APIToken) + "&wait_ms=0"
	for attempt := 0; attempt < 2; attempt++ {
		response := publicCodeTestRequest(t, server, path)
		if response.Code != http.StatusOK {
			t.Fatalf("第 %d 次取码状态为 %d：%s", attempt+1, response.Code, response.Body.String())
		}
		data := publicCodeTestData(t, response)
		if data["code"] != "654321" {
			t.Fatalf("第 %d 次没有返回最新验证码：%+v", attempt+1, data)
		}
	}
}

func newPublicCodeTestServer(t *testing.T, enabled bool) (*Server, *store.Store, domain.Mailbox) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试数据库失败：%v", err)
	}
	t.Cleanup(func() { _ = state.Close() })
	settings := state.Settings()
	settings.EnablePublicCodePage = enabled
	if _, err := state.SaveSettings(settings); err != nil {
		t.Fatalf("保存公共页面设置失败：%v", err)
	}
	mailbox, _, err := state.UpsertMailboxFromRemote("account_fixture", domain.RemoteMailbox{Email: "public-code@icloud.com", IsActive: true}, "")
	if err != nil {
		t.Fatalf("创建测试邮箱失败：%v", err)
	}
	server := New(config.Default(), state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return server, state, mailbox
}

func publicCodeTestRequest(t *testing.T, server *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func publicCodeTestData(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("解析接口响应失败：%v；响应=%s", err, recorder.Body.String())
	}
	data, ok := payload["data"].(map[string]any)
	if !ok {
		t.Fatalf("接口响应缺少 data：%+v", payload)
	}
	return data
}
