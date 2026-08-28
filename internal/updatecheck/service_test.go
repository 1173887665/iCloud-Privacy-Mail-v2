package updatecheck

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"icloud-privacy-mail-v2/internal/buildinfo"
)

func TestCheckUsesRepositoryManifest(t *testing.T) {
	originalVersion := buildinfo.Version
	originalCommit := buildinfo.Commit
	buildinfo.Version = "2.0.0"
	buildinfo.Commit = "test-commit"
	defer func() {
		buildinfo.Version = originalVersion
		buildinfo.Commit = originalCommit
	}()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requestCount++
		if request.URL.Path != "/owner/repository/HEAD/internal/updatecheck/announcements.json" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{
  "schema_version": 1,
  "latest": {
    "version": "2.1.0",
    "name": "2.1.0 源码版",
    "notes": "新增稳定的更新检查。",
    "published_at": "2026-08-28T12:00:00Z",
    "url": "https://github.com/owner/repository/archive/refs/heads/main.zip"
  },
  "announcements": [
    {"id":"notice-1","type":"project","title":"项目公告","summary":"摘要","content":"内容","published_at":"2026-08-28T11:00:00Z"}
  ]
}`)
	}))
	defer server.Close()

	service := New(true, "owner/repository")
	service.rawBaseURL = server.URL
	status := service.Check(context.Background(), true)
	if status.Error != "" {
		t.Fatalf("按仓库公告配置检查失败：%s", status.Error)
	}
	if requestCount != 1 {
		t.Fatalf("更新检查应只请求一次公告配置，实际请求 %d 次", requestCount)
	}
	if !status.UpdateAvailable || status.Latest == nil || status.Latest.Version != "2.1.0" || status.Latest.Source != "config" {
		t.Fatalf("最新版本判断不正确：%+v", status)
	}
	if !containsAnnouncement(status.Announcements, "notice-1") {
		t.Fatalf("未读取公告列表：%+v", status.Announcements)
	}
}

func TestConfiguredLatestRecognizesSameVersion(t *testing.T) {
	latest, available, announcement, err := configuredLatest(buildinfo.Info{Version: "2.0.0-dev"}, latestDocument{
		Version: "2.0.0-dev",
		Name:    "2.0.0 开发版",
		Notes:   "当前版本。",
		URL:     "https://github.com/owner/repository",
	})
	if err != nil {
		t.Fatalf("解析 latest 失败：%v", err)
	}
	if available {
		t.Fatal("相同版本不应提示更新")
	}
	if latest == nil || latest.Source != "config" || announcement != nil {
		t.Fatalf("更新配置结果不正确：latest=%+v announcement=%+v", latest, announcement)
	}
}

func TestCheckRejectsLegacyManifest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"announcements":[]}`)
	}))
	defer server.Close()

	service := New(true, "owner/repository")
	service.rawBaseURL = server.URL
	status := service.Check(context.Background(), true)
	if !strings.Contains(status.Error, "schema_version 应为 1") {
		t.Fatalf("旧格式应返回明确配置错误：%q", status.Error)
	}
}

func TestCheckRequiresLatest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		fmt.Fprint(writer, `{"schema_version":1,"announcements":[]}`)
	}))
	defer server.Close()

	service := New(true, "owner/repository")
	service.rawBaseURL = server.URL
	status := service.Check(context.Background(), true)
	if status.Error != "仓库公告配置缺少 latest" {
		t.Fatalf("缺少 latest 时的错误不正确：%q", status.Error)
	}
}

func TestVersionIsNewer(t *testing.T) {
	testCases := []struct {
		name     string
		current  string
		latest   string
		expected bool
	}{
		{name: "次版本升级", current: "2.0.0", latest: "2.1.0", expected: true},
		{name: "当前版本更高", current: "2.1.0", latest: "2.0.0", expected: false},
		{name: "开发版升级为正式版", current: "2.0.0-dev", latest: "2.0.0", expected: true},
		{name: "正式版高于开发版", current: "2.0.0", latest: "2.0.0-dev", expected: false},
		{name: "高版本开发版不会降级", current: "2.1.0-dev", latest: "2.0.0", expected: false},
		{name: "相同开发版", current: "2.0.0-dev", latest: "2.0.0-dev", expected: false},
		{name: "支持 v 前缀", current: "v2.0.0", latest: "v2.0.1", expected: true},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if actual := versionIsNewer(testCase.current, testCase.latest); actual != testCase.expected {
				t.Fatalf("versionIsNewer(%q, %q) = %v，期望 %v", testCase.current, testCase.latest, actual, testCase.expected)
			}
		})
	}
}

func containsAnnouncement(items []Announcement, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
