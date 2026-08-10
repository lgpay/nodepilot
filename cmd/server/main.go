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

func main() {
	dbPath := flag.String("db", "nodepilot.db", "sqlite db file path (relative to working dir)")
	webDir := flag.String("web-dir", "web", "directory containing web/index.html to serve at /")
	addr := flag.String("addr", ":8080", "listen addresses (comma-separated, e.g. 127.0.0.1:6200,:8080)")
	flag.Parse()

	// 结构化日志（文本格式，带时间/级别/调用点）
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// 密钥必须在 store.Init 之前就绪（token 迁移、AES 加解密依赖）
	config.Init(*dbPath)

	if err := store.Init(*dbPath); err != nil {
		slog.Error("init db failed", "err", err)
		os.Exit(1)
	}
	// 规则镜像的磁盘缓存目录（与 db 同目录下的 rules/）
	server.RulesCacheDir = filepath.Join(filepath.Dir(*dbPath), "rules")
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
