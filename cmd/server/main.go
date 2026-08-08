package main

import (
	"log"

	"nodepilot/internal/server"
	"nodepilot/internal/store"
)

func main() {
	dbPath := "nodepilot.db"
	if err := store.Init(dbPath); err != nil {
		log.Fatalf("init db: %v", err)
	}
	// 首次启动初始化默认管理员 admin / admin123（生产务必修改）
	if err := store.InitAdmin("admin", "admin123"); err != nil {
		log.Fatalf("init admin: %v", err)
	}

	r := server.NewRouter()
	log.Println("[server] NodePilot control plane listening on :8080")
	if err := r.Run(":8080"); err != nil {
		log.Fatalf("server run: %v", err)
	}
}
