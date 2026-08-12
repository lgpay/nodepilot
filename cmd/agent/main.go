package main

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"time"

	"nodepilot/internal/agent"
	"nodepilot/internal/config"

	"github.com/gin-gonic/gin"
)

func main() {
	token := flag.String("token", "", "node token (from control plane registration)")
	addr := flag.String("addr", ":54321", "agent listen address")
	serverURL := flag.String("server", "http://127.0.0.1:8080", "control plane base URL")
	nodeID := flag.String("node-id", "", "node id assigned by control plane")
	configDir := flag.String("config-dir", "/usr/local/xray", "directory to store xray config.json")
	certDir := flag.String("cert-dir", "/opt/nodepilot-agent/certs", "directory to store TLS certificates")
	xrayBin := flag.String("xray", "/usr/local/bin/xray", "path to xray binary")
	flag.Parse()

	if *token == "" || *nodeID == "" {
		log.Fatal("--token and --node-id are required")
	}
	config.InitAgent()

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
	agent.StartHeartbeat(30 * time.Second)
	agent.StartTrafficCollector(60 * time.Second)

	log.Printf("[agent] NodePilot node-agent listening on %s (node=%s)", *addr, *nodeID)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("agent run: %v", err)
	}
}
