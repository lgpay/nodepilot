package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"nodepilot/internal/agent"
	"nodepilot/internal/config"
	"nodepilot/internal/logging"

	"github.com/gin-gonic/gin"
)

// Version 构建版本号，可由构建期注入：
//
//	go build -ldflags "-X main.Version=$(git describe --tags 2>/dev/null || echo dev)"
var Version = "1.2.5"

func main() {
	token := flag.String("token", "", "node token (from control plane registration)")
	addr := flag.String("addr", ":54321", "agent listen address")
	serverURL := flag.String("server", "http://127.0.0.1:8080", "control plane base URL")
	nodeID := flag.String("node-id", "", "node id assigned by control plane")
	configDir := flag.String("config-dir", "/usr/local/xray", "directory to store xray config.json")
	certDir := flag.String("cert-dir", "/opt/nodepilot-agent/certs", "directory to store TLS certificates")
	xrayBin := flag.String("xray", "/usr/local/bin/xray", "path to xray binary")
	heartbeat := flag.Int("heartbeat", 30, "heartbeat interval in seconds")
	flag.Parse()
	logging.Setup()

	if *token == "" || *nodeID == "" {
		log.Fatal("--token and --node-id are required")
	}
	config.InitAgent()
	agent.SetVersion(Version)

	agent.SetCertDir(*certDir)
	agent.SetConfig(agent.AgentConfig{
		Token:     *token,
		Addr:      *addr,
		ServerURL: *serverURL,
		NodeID:    *nodeID,
		ConfigDir: *configDir,
		CertDir:   *certDir,
	})
	agent.SetXrayBin(*xrayBin)

	// 开机自动拉起 xray：本地已有已落盘的配置则直接用其启动，避免节点重启后代理停摆
	cfgFile := filepath.Join(*configDir, "config.json")
	if _, err := os.Stat(cfgFile); err == nil {
		if err := agent.Restart(cfgFile); err != nil {
			log.Printf("[agent] auto-start xray failed: %v", err)
		} else {
			log.Printf("[agent] xray auto-started with existing config: %s", cfgFile)
		}
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	agent.RegisterRoutes(r)
	agent.StartHeartbeat(time.Duration(*heartbeat) * time.Second)
	agent.StartTrafficCollector(60 * time.Second)

	srv := &http.Server{Addr: *addr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		log.Printf("[agent] NodePilot node-agent listening on %s (node=%s)", *addr, *nodeID)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("agent run: %v", err)
		}
	}()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("[agent] graceful shutdown failed: %v", err)
	}
	agent.StopBackgroundWorkers()
}
