package main

import (
	"flag"
	"log"
	"time"

	"nodepilot/internal/agent"

	"github.com/gin-gonic/gin"
)

func main() {
	token := flag.String("token", "", "node token (from control plane registration)")
	addr := flag.String("addr", ":54321", "agent listen address")
	serverURL := flag.String("server", "http://127.0.0.1:8080", "control plane base URL")
	nodeID := flag.String("node-id", "", "node id assigned by control plane")
	configDir := flag.String("config-dir", "/usr/local/xray", "directory to store xray config.json")
	xrayBin := flag.String("xray", "/usr/local/bin/xray", "path to xray binary")
	flag.Parse()

	if *token == "" || *nodeID == "" {
		log.Fatal("--token and --node-id are required")
	}

	agent.SetConfig(agent.AgentConfig{
		Token:     *token,
		Addr:      *addr,
		ServerURL: *serverURL,
		NodeID:    *nodeID,
		ConfigDir: *configDir,
	})
	agent.SetXrayBin(*xrayBin)

	r := gin.Default()
	agent.RegisterRoutes(r)
	agent.StartHeartbeat(30 * time.Second)

	log.Printf("[agent] NodePilot node-agent listening on %s (node=%s)", *addr, *nodeID)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("agent run: %v", err)
	}
}
