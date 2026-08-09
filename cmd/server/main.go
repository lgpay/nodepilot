package main

import (
	"flag"
	"log"
	"path/filepath"

	"nodepilot/internal/server"
	"nodepilot/internal/store"
)

func main() {
	dbPath := flag.String("db", "nodepilot.db", "sqlite db file path (relative to working dir)")
	webDir := flag.String("web-dir", "web", "directory containing web/index.html to serve at /")
	addr := flag.String("addr", ":8080", "listen address")
	flag.Parse()

	if err := store.Init(*dbPath); err != nil {
		log.Fatalf("init db: %v", err)
	}
	// 规则镜像的磁盘缓存目录（与 db 同目录下的 rules/）
	server.RulesCacheDir = filepath.Join(filepath.Dir(*dbPath), "rules")
	// 首次启动初始化默认管理员 admin / admin123（生产务必修改）
	if err := store.InitAdmin("admin", "admin123"); err != nil {
		log.Fatalf("init admin: %v", err)
	}

	r := server.NewRouter(*webDir)
	server.StartProbeScheduler()
	server.StartCertRenewScheduler()
	server.StartAlertScheduler()
	log.Println("[server] NodePilot control plane listening on", *addr)
	if err := r.Run(*addr); err != nil {
		log.Fatalf("server run: %v", err)
	}
}
