package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"icloud-privacy-mail-v2/internal/buildinfo"
	"icloud-privacy-mail-v2/internal/config"
	"icloud-privacy-mail-v2/internal/httpapi"
	"icloud-privacy-mail-v2/internal/store"
)

type launchOptions struct {
	ConfigPath string
	Host       string
	Port       int
	DataPath   string
}

func main() {
	options := launchOptions{}
	showMenu := false
	flag.StringVar(&options.ConfigPath, "config", "config.json", "配置文件路径")
	flag.StringVar(&options.Host, "host", "", "覆盖监听地址")
	flag.IntVar(&options.Port, "port", 0, "覆盖监听端口")
	flag.StringVar(&options.DataPath, "data", "", "覆盖 SQLite 数据库路径")
	flag.BoolVar(&showMenu, "menu", false, "显示交互式启动菜单")
	flag.Parse()

	if (len(os.Args) == 1 && stdinIsTerminal()) || showMenu {
		if !runMenu(&options) {
			return
		}
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	if err := runServer(options, logger); err != nil {
		logger.Error("服务启动失败", "错误", err)
		os.Exit(1)
	}
}

func runServer(options launchOptions, logger *slog.Logger) error {
	cfg, err := effectiveConfig(options)
	if err != nil {
		return fmt.Errorf("加载配置失败：%w", err)
	}
	state, err := store.Open(cfg.DataPath)
	if err != nil {
		return fmt.Errorf("打开数据存储失败：%w", err)
	}
	defer state.Close()

	handler := httpapi.New(cfg, state, logger)
	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 全量邮件同步可能需要遍历多个 Apple 主号和完整收件箱，给长任务保留足够的响应时间。
		WriteTimeout:   30 * time.Minute,
		IdleTimeout:    60 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	handler.StartBackground(ctx)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	current := buildinfo.Current()
	logger.Info("iCloud Privacy Mail v2 已启动", "地址", "http://"+server.Addr, "版本", current.Version, "提交", current.Commit, "配置", options.ConfigPath, "数据", cfg.DataPath)
	err = server.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func effectiveConfig(options launchOptions) (config.Config, error) {
	cfg, err := config.Load(strings.TrimSpace(options.ConfigPath))
	if err != nil {
		return config.Config{}, err
	}
	if strings.TrimSpace(options.Host) != "" {
		cfg.Host = strings.TrimSpace(options.Host)
	}
	if options.Port > 0 {
		cfg.Port = options.Port
	}
	if strings.TrimSpace(options.DataPath) != "" {
		cfg.DataPath = strings.TrimSpace(options.DataPath)
	}
	return cfg, nil
}

func runMenu(options *launchOptions) bool {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println()
		fmt.Println("========================================")
		fmt.Println("  iCloud Privacy Mail v2 启动菜单")
		fmt.Println("========================================")
		fmt.Println("  1. 使用当前配置启动服务")
		fmt.Println("  2. 自定义地址、端口和数据文件后启动")
		fmt.Println("  3. 查看当前生效配置")
		fmt.Println("  4. 显示常用命令")
		fmt.Println("  0. 退出")
		fmt.Print("请选择 [1]：")
		choice := readMenuLine(reader)
		if choice == "" {
			choice = "1"
		}
		switch choice {
		case "1":
			return true
		case "2":
			customizeLaunchOptions(reader, options)
			return true
		case "3":
			printEffectiveConfig(*options)
		case "4":
			printCommonCommands()
		case "0", "q", "quit", "exit":
			fmt.Println("已退出。")
			return false
		default:
			fmt.Println("选项不存在，请重新选择。")
		}
	}
}

func customizeLaunchOptions(reader *bufio.Reader, options *launchOptions) {
	fmt.Printf("配置文件 [%s]：", options.ConfigPath)
	if value := readMenuLine(reader); value != "" {
		options.ConfigPath = value
	}
	fmt.Printf("监听地址 [%s]：", displayDefault(options.Host, "使用配置文件"))
	if value := readMenuLine(reader); value != "" {
		options.Host = value
	}
	fmt.Printf("监听端口 [%s]：", displayPort(options.Port))
	if value := readMenuLine(reader); value != "" {
		port, err := strconv.Atoi(value)
		if err == nil && port > 0 && port <= 65535 {
			options.Port = port
		} else {
			fmt.Println("端口格式不正确，将继续使用配置文件中的端口。")
		}
	}
	fmt.Printf("数据库 [%s]：", displayDefault(options.DataPath, "使用配置文件"))
	if value := readMenuLine(reader); value != "" {
		options.DataPath = value
	}
}

func printEffectiveConfig(options launchOptions) {
	cfg, err := effectiveConfig(options)
	if err != nil {
		fmt.Println("读取配置失败：", err)
		return
	}
	fmt.Println()
	fmt.Println("当前生效配置：")
	fmt.Println("  配置文件：", options.ConfigPath)
	fmt.Printf("  服务地址：http://%s:%d\n", cfg.Host, cfg.Port)
	fmt.Println("  数据库：", cfg.DataPath)
	fmt.Println("  邮件监听：", enabledText(cfg.MailWatcherEnabled))
	fmt.Println("  Apple 保活：", enabledText(cfg.AppleAccountKeepAliveEnabled))
}

func printCommonCommands() {
	fmt.Println()
	fmt.Println("常用命令：")
	fmt.Println("  直接启动：go run main.go")
	fmt.Println("  指定配置：go run main.go -config config.json")
	fmt.Println("  显示菜单：go run main.go -menu")
	fmt.Println("  开发模式：./scripts/dev.sh")
	fmt.Println("  完整构建：./scripts/build.sh")
}

func readMenuLine(reader *bufio.Reader) string {
	value, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(value) == "" {
		return "0"
	}
	return strings.TrimSpace(value)
}

func stdinIsTerminal() bool {
	info, err := os.Stdin.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func displayDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func displayPort(port int) string {
	if port <= 0 {
		return "使用配置文件"
	}
	return strconv.Itoa(port)
}

func enabledText(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}
