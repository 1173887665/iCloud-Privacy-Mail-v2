package protocol

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPhoneSecurityCodeUsesBootArgsPhoneAndReferrerQuery(t *testing.T) {
	var requestPath string
	var requestBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			w.Header().Set("scnt", "boot-scnt")
			_, _ = io.WriteString(w, `<script type="application/json" class="boot_args">{"direct":{"referrerQuery":"?ref=boot","twoSV":{"phoneNumberVerification":{"trustedPhoneNumbers":[{"id":7,"nonFTEU":true}]}}}}</script>`)
		case http.MethodPost:
			requestPath = r.URL.RequestURI()
			body, _ := io.ReadAll(r.Body)
			requestBody = string(body)
			if r.URL.RawQuery == "ref=boot" && strings.Contains(requestBody, `"id":7`) {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer server.Close()

	client := &AppleAuthClient{httpClient: server.Client()}
	session := &appleAuthSession{
		Endpoints: appleAuthEndpoints{Auth: server.URL},
		UserAgent: appleAuthUserAgent,
	}
	if err := client.refreshAuthState(context.Background(), session); err != nil {
		t.Fatalf("刷新认证状态失败：%v", err)
	}
	if err := client.validatePhoneSecurityCode(context.Background(), session, "228584", nil); err != nil {
		t.Fatalf("短信验证失败：path=%s body=%s err=%v", requestPath, requestBody, err)
	}
	if requestPath != "/verify/phone/securitycode?ref=boot" {
		t.Fatalf("短信验证路径不正确：%s", requestPath)
	}
}

func TestSubmit2FARetriesDomainSwitchAfterCodeValidation(t *testing.T) {
	session := &appleAuthSession{Endpoints: appleAuthEndpointsForHost("www.icloud.com.cn")}
	var calls int
	result, err := retryAppleDomainRedirect(session, func() (ICloudSession, error) {
		calls++
		if calls == 1 {
			return ICloudSession{}, appleDomainRedirectError{DomainToUse: "iCloud.com", Host: "www.icloud.com"}
		}
		return ICloudSession{Host: session.Endpoints.Host}, nil
	})
	if err != nil {
		t.Fatalf("域切换后登录态重试失败：%v", err)
	}
	if calls != 2 {
		t.Fatalf("域切换重试次数不正确：%d", calls)
	}
	if session.Endpoints.Host != "www.icloud.com" || result.Host != "www.icloud.com" {
		t.Fatalf("未切换到 Apple 返回的域：session=%s result=%s", session.Endpoints.Host, result.Host)
	}
}

type appleAuthRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn appleAuthRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestSubmit2FADoesNotResubmitCodeAfterDomainSwitch(t *testing.T) {
	var verificationCalls int
	client := &AppleAuthClient{httpClient: &http.Client{Transport: appleAuthRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		status := http.StatusNoContent
		body := ""
		switch {
		case strings.HasSuffix(req.URL.Path, "/verify/phone/securitycode"):
			verificationCalls++
			if verificationCalls > 1 {
				status = http.StatusUnauthorized
			}
		case strings.HasSuffix(req.URL.Path, "/accountLogin") && strings.Contains(req.URL.Host, ".com.cn"):
			status = http.StatusFound
			body = `{"domainToUse":"iCloud.com"}`
		case strings.HasSuffix(req.URL.Path, "/accountLogin"):
			status = http.StatusBadGateway
			body = `{"error":"stop_after_domain_retry"}`
		}
		return &http.Response{
			StatusCode: status,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}}
	session := &appleAuthSession{
		Endpoints:       appleAuthEndpointsForHost("www.icloud.com.cn"),
		SessionToken:    "transaction-token",
		Scnt:            "transaction-scnt",
		SessionID:       "transaction-session",
		TwoFactorMethod: appleTwoFactorMethodPhone,
		TwoFactorPhone:  []byte(`{"id":1,"nonFTEU":true}`),
	}
	_, _ = client.Submit2FA(context.Background(), appleAuthPending{Session: session}, "228584")
	if verificationCalls != 1 {
		t.Fatalf("域切换重复提交了短信验证码：calls=%d", verificationCalls)
	}
}

func TestRequestPhoneSecurityCodeAccepts412AfterSMSWasSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"trustedPhoneNumbers":[{"id":7,"nonFTEU":true,"pushMode":"sms","lastTwoDigits":"41"}]}`))
	}))
	defer server.Close()

	client := &AppleAuthClient{httpClient: server.Client()}
	session := &appleAuthSession{
		Endpoints: appleAuthEndpoints{Auth: server.URL},
		UserAgent: appleAuthUserAgent,
	}
	if err := client.requestPhoneSecurityCode(context.Background(), session, nil); err != nil {
		t.Fatalf("412 短信发送结果不应被判定为失败：%v", err)
	}
	if !strings.Contains(string(session.TwoFactorPhone), `"id":7`) {
		t.Fatalf("未保存 Apple 返回的受信任手机号：%s", session.TwoFactorPhone)
	}
}

func TestRequestPhoneSecurityCodeRejects412WithoutPhoneDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPreconditionFailed)
		_, _ = w.Write([]byte(`{"error":"precondition_failed"}`))
	}))
	defer server.Close()

	client := &AppleAuthClient{httpClient: server.Client()}
	session := &appleAuthSession{Endpoints: appleAuthEndpoints{Auth: server.URL}}
	if err := client.requestPhoneSecurityCode(context.Background(), session, nil); err == nil {
		t.Fatal("缺少受信任手机号信息的 412 响应不应被当作短信已发送")
	}
}
