package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/domain"
	"icloud-privacy-mail-v2/internal/protocol"
	"icloud-privacy-mail-v2/internal/store"
)

func TestAppleKeepAliveScanIntervalPollsBeforeBaseInterval(t *testing.T) {
	if got := appleKeepAliveScanInterval(3 * time.Minute); got != 30*time.Second {
		t.Fatalf("3 分钟基础周期的扫描间隔为 %s，期望 30s", got)
	}
	if got := appleKeepAliveScanInterval(12 * time.Second); got != 5*time.Second {
		t.Fatalf("12 秒基础周期的扫描间隔为 %s，期望 5s", got)
	}
}

func TestAPIResponsesDisableCaching(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试状态失败：%v", err)
	}
	server := New(config.Default(), state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	recorder := httptest.NewRecorder()
	server.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("API 缓存策略为 %q，期望 no-store", got)
	}
}

func TestAppleKeepAliveRoundSavesUpdatedState(t *testing.T) {
	state, session := newKeepAliveTestState(t, true, time.Now().Add(-time.Hour))
	cfg := config.Default()
	cfg.AppleAccountKeepAliveJitterPercent = 0
	server := New(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var calls int
	server.keepAliveState = func(_ context.Context, loginState domain.LoginState) (domain.LoginState, error) {
		calls++
		loginState.Scnt = "fresh-scnt"
		loginState.APIKey = "fresh-key"
		return loginState, nil
	}

	server.keepAliveAppleRound(context.Background(), 3*time.Minute)

	if calls != 1 {
		t.Fatalf("保活调用次数为 %d，期望 1", calls)
	}
	updated, ok := state.ICloudSessionByAccountID(session.AccountID)
	if !ok {
		t.Fatal("没有找到保活后的 Apple 登录态")
	}
	loginState, ok := protocol.LoginStateForKind(updated, domain.LoginStateAppleAccount)
	if !ok || loginState.Scnt != "fresh-scnt" || loginState.APIKey != "fresh-key" || !loginState.LastCheckOK || loginState.LastStatusMessage != "Apple Account 登录态保活成功" {
		t.Fatalf("保活后的登录态不正确：%+v，存在=%t", loginState, ok)
	}
	assertKeepAliveEvent(t, state, "开始 Apple 登录态保活：keepalive@example.com")
	assertKeepAliveEvent(t, state, "Apple 登录态保活成功：keepalive@example.com")
}

func TestAppleKeepAliveRoundRecordsTransientFailure(t *testing.T) {
	state, _ := newKeepAliveTestState(t, true, time.Now().Add(-time.Hour))
	server := New(config.Default(), state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server.keepAliveState = func(_ context.Context, loginState domain.LoginState) (domain.LoginState, error) {
		return loginState, errors.New("连接暂时超时")
	}

	server.keepAliveAppleRound(context.Background(), 3*time.Minute)

	assertKeepAliveEvent(t, state, "开始 Apple 登录态保活：keepalive@example.com")
	assertKeepAliveEvent(t, state, "Apple 登录态保活临时失败：keepalive@example.com")
}

func TestAppleKeepAliveRoundStopsSilentlyWhenServiceIsCanceled(t *testing.T) {
	state, _ := newKeepAliveTestState(t, true, time.Now().Add(-time.Hour))
	server := New(config.Default(), state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	called := make(chan struct{})
	server.keepAliveState = func(ctx context.Context, loginState domain.LoginState) (domain.LoginState, error) {
		close(called)
		<-ctx.Done()
		return loginState, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		server.keepAliveAppleRound(ctx, 3*time.Minute)
		close(done)
	}()
	<-called
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("取消服务后保活轮次没有及时退出")
	}
	for _, event := range state.Dashboard().Events {
		if strings.Contains(event.Message, "保活临时失败") || strings.Contains(event.Message, "登录态已失效") {
			t.Fatalf("服务关闭不应记录保活失败：%s", event.Message)
		}
	}
}

func TestAppleKeepAliveRoundSkipsFreshAndFailedStates(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		lastCheckOK   bool
		lastCheckedAt time.Time
	}{
		{name: "fresh_state", lastCheckOK: true, lastCheckedAt: time.Now()},
		{name: "failed_state", lastCheckOK: false, lastCheckedAt: time.Now().Add(-time.Hour)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state, _ := newKeepAliveTestState(t, testCase.lastCheckOK, testCase.lastCheckedAt)
			server := New(config.Default(), state, slog.New(slog.NewTextHandler(io.Discard, nil)))
			server.keepAliveState = func(_ context.Context, loginState domain.LoginState) (domain.LoginState, error) {
				t.Fatal("不应调用尚未到期或已确认失效的登录态")
				return loginState, nil
			}
			server.keepAliveAppleRound(context.Background(), 3*time.Minute)
		})
	}
}

func TestRunAppleKeepAliveChecksImmediately(t *testing.T) {
	state, _ := newKeepAliveTestState(t, true, time.Now().Add(-time.Hour))
	cfg := config.Default()
	cfg.AppleAccountKeepAliveMS = 180000
	cfg.AppleAccountKeepAliveJitterPercent = 0
	server := New(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	called := make(chan struct{}, 1)
	server.keepAliveState = func(_ context.Context, loginState domain.LoginState) (domain.LoginState, error) {
		called <- struct{}{}
		return loginState, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	go func() {
		server.runAppleKeepAlive(ctx)
		close(finished)
	}()

	select {
	case <-called:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("保活服务启动后没有立即扫描登录态")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("取消后保活服务没有及时停止")
	}
}

func TestMailWatcherSettingTakesEffectWithoutRestart(t *testing.T) {
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试状态失败：%v", err)
	}
	cfg := config.Default()
	server := New(cfg, state, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	server.StartBackground(ctx)
	t.Cleanup(cancel)

	waitForWatcherStatus(t, server, func(status bool, enabled bool, groups int) bool {
		return status && !enabled && groups == 0
	})
	settings := state.Settings()
	settings.EnableMailWatcher = true
	if _, err := state.SaveSettings(settings); err != nil {
		t.Fatalf("开启测试邮件监听失败：%v", err)
	}
	server.watcher.Wake("")
	waitForWatcherStatus(t, server, func(status bool, enabled bool, groups int) bool {
		return status && enabled && groups == 0
	})

	cancel()
	waitForWatcherStatus(t, server, func(status bool, _ bool, _ int) bool { return !status })
}

func waitForWatcherStatus(t *testing.T, server *Server, match func(running bool, enabled bool, groups int) bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		status := server.watcher.Snapshot()
		if match(status.Running, status.Enabled, status.GroupCount) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	status := server.watcher.Snapshot()
	t.Fatalf("等待邮件监听状态超时：%+v", status)
}

func assertKeepAliveEvent(t *testing.T, state *store.Store, text string) {
	t.Helper()
	for _, event := range state.Dashboard().Events {
		if strings.Contains(event.Message, text) {
			return
		}
	}
	t.Fatalf("运行记录中没有找到 %q", text)
}

func newKeepAliveTestState(t *testing.T, lastCheckOK bool, lastCheckedAt time.Time) (*store.Store, domain.ICloudSession) {
	t.Helper()
	state, err := store.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("创建测试状态失败：%v", err)
	}
	settings := state.Settings()
	settings.EnableAppleKeepAlive = true
	if _, err := state.SaveSettings(settings); err != nil {
		t.Fatalf("启用测试保活设置失败：%v", err)
	}
	session, err := state.SaveICloudSession(domain.ICloudSession{
		AppleID: "keepalive@example.com",
		SavedAt: time.Now(),
		LoginStates: []domain.LoginState{{
			Kind:          domain.LoginStateAppleAccount,
			Scnt:          "saved-scnt",
			APIKey:        "saved-key",
			Cookies:       []domain.SessionCookie{{Name: "session", Value: "fixture", Domain: "apple.com", Path: "/"}},
			LastCheckedAt: lastCheckedAt,
			LastCheckOK:   lastCheckOK,
		}},
	})
	if err != nil {
		t.Fatalf("保存测试 Apple 登录态失败：%v", err)
	}
	return state, session
}
