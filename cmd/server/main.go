package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"nodepilot/internal/config"
	"nodepilot/internal/server"
	"nodepilot/internal/store"
)

// Version 构建版本号，可由构建期注入：
//
//	go build -ldflags "-X main.Version=$(git describe --tags 2>/dev/null || echo dev)" ./cmd/server
var Version = "0.1.3"

func main() {
	dbPath := flag.String("db", "nodepilot.db", "sqlite db file path (relative to working dir)")
	webDir := flag.String("web-dir", "web", "directory containing web/index.html to serve at /")
	addr := flag.String("addr", ":8080", "listen addresses (comma-separated, e.g. 127.0.0.1:6200,:8080)")
	rulesDir := flag.String("rules-dir", "rules", "directory containing ACL4SSR_Online.ini (ACL4SSR rule template, synced by GitHub Actions)")
	flag.Parse()

	// 结构化日志（文本格式，带时间/级别/调用点）
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	slog.Info("[server] NodePilot control plane starting", "version", Version)

	// 密钥必须在 store.Init 之前就绪（token 迁移、AES 加解密依赖）
	config.Init(*dbPath)

	// 安全提示：TLS 校验默认关闭（MVP 兼容），生产环境应启用
	if !config.AgentTLSVerify {
		slog.Warn("⚠️  NP_AGENT_TLS_VERIFY 未设为 true：管理端→agent 通信将跳过 TLS 证书校验，存在中间人风险！生产环境请设置 NP_AGENT_TLS_VERIFY=true 并配合可信证书")
	}

	if err := store.Init(*dbPath); err != nil {
		slog.Error("init db failed", "err", err)
		os.Exit(1)
	}
	// 规则镜像的磁盘缓存目录（与 db 同目录下的 rules/）
	server.RulesCacheDir = filepath.Join(filepath.Dir(*dbPath), "rules")
	// ACL4SSR 分组/规则模板：优先加载仓库内 rules/ACL4SSR_Online.ini（GitHub Actions 同步），
	// 缺失或解析失败时回退内置静态快照
	server.InitACLTemplate(*rulesDir)
	// 首次启动初始化默认管理员（随机密码，强制首次登录修改）
	pwd, err := store.InitAdmin("admin")
	if err != nil {
		slog.Error("init admin failed", "err", err)
		os.Exit(1)
	}
	if pwd != "" {
		slog.Warn("================================================================")
		slog.Warn("初始管理员已创建", "username", "admin", "password", pwd)
		slog.Warn("请立即登录并在「修改密码」中更换此随机密码！")
		slog.Warn("================================================================")
	}

	r := server.NewRouter(*webDir)
	server.StartProbeScheduler()
	server.StartCertRenewScheduler()
	server.StartAlertScheduler()

	// 支持逗号分隔的多个监听地址（如反代走 127.0.0.1:6200，节点直连保留 :8080）
	var servers []*http.Server
	for _, a := range strings.Split(*addr, ",") {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		srv := &http.Server{Addr: a, Handler: r, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go func(s *http.Server) {
			slog.Info("[server] NodePilot control plane listening", "addr", s.Addr)
			if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				slog.Error("server run failed", "err", err, "addr", s.Addr)
				os.Exit(1)
			}
		}(srv)
	}

	// 优雅关闭：捕获 SIGINT/SIGTERM，等待在途请求完成
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("[server] shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, s := range servers {
		if err := s.Shutdown(ctx); err != nil {
			slog.Error("graceful shutdown failed", "err", err)
		}
	}
	slog.Info("[server] stopped")
}
