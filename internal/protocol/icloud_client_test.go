package protocol

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestAppleAccountEmptyUnauthorizedResponseIsAuthFailure(t *testing.T) {
	err := appleAccountAPIError(http.StatusUnauthorized, nil, "刷新管理 token")
	code, message, retryable := ErrorDetails(err)
	if code != "apple_account_auth_failed" || !retryable {
		t.Fatalf("空响应 401 的错误分类为 code=%q retryable=%t，期望登录态失效", code, retryable)
	}
	if message == "" {
		t.Fatal("空响应 401 缺少错误说明")
	}
}

func TestAppleAccountSettingsIneligibilityIsNotRetryable(t *testing.T) {
	payload := []byte(`{"active":false,"exists":false,"ineligibilityReason":"unknown","ineligibilityType":"settings","newToPrivateEmail":"true","useOslOStyle":false}`)
	err := appleAccountAPIError(http.StatusPreconditionFailed, payload, "生成候选隐私邮箱")
	code, message, retryable := ErrorDetails(err)
	t.Logf("code=%s retryable=%t message=%s", code, retryable, message)
	if code != "apple_account_hme_settings" {
		t.Fatalf("HTTP 412 settings 应归类为账户设置错误，实际 code=%q", code)
	}
	if retryable {
		t.Fatal("账户资格设置错误不应标记为可重试")
	}
	if !strings.Contains(message, "隐藏我的电子邮件") {
		t.Fatalf("账户设置错误缺少可执行提示：%s", message)
	}
}

func TestCanonicalMailIDNormalizesMessageIDAcrossReadPaths(t *testing.T) {
	receivedAt := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	fromIMAP := canonicalMailID("<Message-42@Example.COM>", "sender@example.com", "主题", receivedAt)
	fromWeb := canonicalMailID("message-42@example.com", "other@example.com", "不同主题", receivedAt.Add(time.Hour))
	if fromIMAP != fromWeb || fromIMAP != "message-id:message-42@example.com" {
		t.Fatalf("跨路径 Message-ID 规范化结果不一致：IMAP=%q，Web=%q", fromIMAP, fromWeb)
	}
}

func TestMailHeaderValueReadsFoldedMessageID(t *testing.T) {
	header := "From: sender@example.com\r\nMessage-ID:\r\n <folded-42@example.com>\r\nSubject: 测试"
	if value := mailHeaderValue(header, "Message-ID"); value != "<folded-42@example.com>" {
		t.Fatalf("长邮件头 Message-ID 解析错误：%q", value)
	}
}

func TestMailThreadSearchLimitUsesWebValidatedBoundForFullScan(t *testing.T) {
	options := MailSyncOptions{FullScan: true, Limit: 20}
	if limit := mailThreadSearchLimit(mailFolder{MessageCount: 738}, options); limit != 1000 {
		t.Fatalf("全量 Web 同步上限不正确：%d", limit)
	}
	if limit := mailThreadSearchLimit(mailFolder{MessageCount: 5000}, options); limit != 1000 {
		t.Fatalf("全量 Web 同步不应直接使用文件夹邮件数：%d", limit)
	}
}

func TestMailThreadSearchLimitKeepsIncrementalBound(t *testing.T) {
	if limit := mailThreadSearchLimit(mailFolder{MessageCount: 738}, MailSyncOptions{Limit: 20}); limit != 20 {
		t.Fatalf("增量同步限制不正确：%d", limit)
	}
}

func TestMailThreadSearchBodyUsesBrowserFullScanMode(t *testing.T) {
	body := mailThreadSearchBody(mailFolder{Name: "INBOX", MessageCount: 53}, 1000, true)
	if body["responseType"] != "THREAD_ID_AND_DATE" || body["includeFolderStatus"] != true || body["maxResults"] != 1000 {
		t.Fatalf("全量 Web 检索请求体不正确：%+v", body)
	}
	headers, ok := body["sessionHeaders"].(map[string]any)
	if !ok || headers["folder"] != "INBOX" || headers["modseq"] != nil || headers["threadmodseq"] != nil {
		t.Fatalf("首次全量 Web 检索的会话头不正确：%+v", body["sessionHeaders"])
	}
}

func TestMailThreadSearchBodyKeepsDigestModeForIncrementalSync(t *testing.T) {
	body := mailThreadSearchBody(mailFolder{Name: "INBOX"}, 20, false)
	if body["responseType"] != "THREAD_DIGEST" || body["includeFolderStatus"] != false || body["maxResults"] != 20 {
		t.Fatalf("增量 Web 检索请求体不正确：%+v", body)
	}
}
