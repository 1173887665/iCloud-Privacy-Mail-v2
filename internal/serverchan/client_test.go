package serverchan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientSendPostsServerChanPayload(t *testing.T) {
	var received map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/SCT_TEST.send" {
			t.Fatalf("请求不正确：%s %s", r.Method, r.URL.Path)
		}
		if contentType := r.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
			t.Fatalf("内容类型不正确：%s", contentType)
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"","data":{"pushid":"push-1","readkey":"read-1"}}`))
	}))
	defer server.Close()

	client := NewClient()
	client.Endpoint = server.URL
	result, err := client.Send(context.Background(), Options{SendKey: "SCT_TEST", HideIP: true}, Message{Title: "测试标题", Desp: "测试内容", Short: "摘要"})
	if err != nil {
		t.Fatalf("发送失败：%v", err)
	}
	if result.PushID != "push-1" || result.ReadKey != "read-1" {
		t.Fatalf("推送结果不正确：%+v", result)
	}
	if received["title"] != "测试标题" || received["desp"] != "测试内容" || received["short"] != "摘要" || received["noip"] != float64(1) {
		t.Fatalf("推送参数不正确：%+v", received)
	}
	if _, exists := received["channel"]; exists {
		t.Fatalf("推送不应覆盖 Server 酱网站配置的默认通道：%+v", received)
	}
	if _, exists := received["openid"]; exists {
		t.Fatalf("推送不应指定 OpenID：%+v", received)
	}
}

func TestClientSendReturnsBusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"code":40001,"message":"SendKey 错误","data":null}`))
	}))
	defer server.Close()
	client := NewClient()
	client.Endpoint = server.URL
	_, err := client.Send(context.Background(), Options{SendKey: "SCT_BAD"}, Message{Title: "测试"})
	if err == nil || !strings.Contains(err.Error(), "SendKey 错误") {
		t.Fatalf("业务错误不正确：%v", err)
	}
}

func TestClientSendTruncatesTextLimits(t *testing.T) {
	var title, short string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]string
		_ = json.NewDecoder(r.Body).Decode(&payload)
		title, short = payload["title"], payload["short"]
		_, _ = w.Write([]byte(`{"code":0,"data":{}}`))
	}))
	defer server.Close()
	client := NewClient()
	client.Endpoint = server.URL
	_, err := client.Send(context.Background(), Options{SendKey: "SCT_TEST"}, Message{Title: strings.Repeat("标", 40), Short: strings.Repeat("短", 80)})
	if err != nil {
		t.Fatal(err)
	}
	if len([]rune(title)) != 32 || len([]rune(short)) != 64 {
		t.Fatalf("长度截断不正确：title=%d short=%d", len([]rune(title)), len([]rune(short)))
	}
}

func TestClientSendDoesNotExposeSendKeyInNetworkError(t *testing.T) {
	const sendKey = "SCT-sensitive-key"
	client := NewClient()
	client.HTTPClient = &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, context.DeadlineExceeded
	})}
	_, err := client.Send(context.Background(), Options{SendKey: sendKey}, Message{Title: "测试"})
	if err == nil || strings.Contains(err.Error(), sendKey) {
		t.Fatalf("网络错误泄露了 SendKey：%v", err)
	}
}
