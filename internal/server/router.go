package server

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/auth"
)

// NewRouter 构建管理端 HTTP 路由（/api/v1）
// webDir 非空时在该目录下提供 index.html 作为 Web 管理界面（运行目录与项目目录隔离）
func NewRouter(webDir string) *gin.Engine {
	r := gin.Default()
	v1 := r.Group("/api/v1")

	// 公开：管理员登录
	v1.POST("/auth/login", LoginHandler)
	v1.POST("/auth/logout", AuthMiddleware(), LogoutHandler)
	v1.POST("/auth/change-password", AuthMiddleware(), ChangePassword)

	// 管理员受保护接口
	authed := v1.Group("")
	authed.Use(AuthMiddleware())
	{
		authed.GET("/nodes", ListNodes)
		authed.POST("/nodes", CreateNode)
		authed.GET("/nodes/:id", GetNode)
		authed.GET("/nodes/:id/install", NodeInstall)
		authed.PATCH("/nodes/:id", UpdateNode)
		authed.DELETE("/nodes/:id", DeleteNode)

		authed.GET("/nodes/:id/inbounds", ListInbounds)
		authed.GET("/inbounds", ListAllInbounds)
		authed.POST("/nodes/:id/inbounds", CreateInbound)
		authed.PUT("/inbounds/:id", UpdateInbound)
		authed.DELETE("/inbounds/:id", DeleteInbound)

		authed.GET("/inbounds/:id/clients", ListClients)
		authed.POST("/inbounds/:id/clients", CreateClient)
		authed.PUT("/clients/:id", UpdateClient)
		authed.DELETE("/clients/:id", DeleteClient)

		authed.POST("/nodes/:id/config/sync", SyncNode)
		authed.GET("/nodes/:id/config/versions", ListConfigVersions)

		authed.GET("/subscriptions", ListSubscriptions)
		authed.POST("/subscriptions", CreateSubscription)
		authed.GET("/subscriptions/:id", GetSubscriptionDetail)
		authed.PATCH("/subscriptions/:id", UpdateSubscription)
		authed.DELETE("/subscriptions/:id", DeleteSubscription)

		// 全局 TLS 证书（管理端签发泛域名 + 分发）
		authed.GET("/certs", ListCerts)
		authed.POST("/certs", CreateCert)
		authed.GET("/certs/:id", GetCert)
		authed.DELETE("/certs/:id", DeleteCert)
		authed.POST("/certs/:id/renew", RenewCert)
		authed.POST("/certs/:id/distribute", DistributeCert)

		// 流量统计聚合
		authed.GET("/stats/overview", StatsOverview)

		// 预警通知渠道（邮件 / 企业微信 / Telegram）
		authed.GET("/notifiers", ListNotifiers)
		authed.POST("/notifiers", CreateNotifier)
		authed.GET("/notifiers/:id", GetNotifier)
		authed.PATCH("/notifiers/:id", UpdateNotifier)
		authed.DELETE("/notifiers/:id", DeleteNotifier)
		authed.POST("/notifiers/:id/test", TestNotifier)
	}

	// 节点 token 受保护接口（agent 上报）
	nodeAuth := v1.Group("")
	nodeAuth.Use(NodeTokenMiddleware())
	{
		nodeAuth.POST("/nodes/:id/heartbeat", Heartbeat)
		nodeAuth.POST("/nodes/:id/traffic", Traffic) // MVP 占位
	}

	// 对外订阅端点（token 校验，非 JWT）
	v1.GET("/sub/:token", GetSubscription)
	v1.GET("/qr/:token", GetSubscriptionQR)

	// Web 管理界面（单页 index.html）
	if webDir != "" {
		if idx := filepath.Join(webDir, "index.html"); fileExists(idx) {
			r.StaticFile("/", idx)
		}
	}

	return r
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// AuthMiddleware 校验管理员 JWT
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authz := c.GetHeader("Authorization")
		t := strings.TrimPrefix(authz, "Bearer ")
		if t == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "missing token"})
			return
		}
		if _, err := auth.ParseJWT(t); err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid token"})
			return
		}
		c.Next()
	}
}

// NodeTokenMiddleware 校验节点 Bearer token（与管理端存储的明文 token 比对）
func NodeTokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := c.Param("id")
		node, err := getNode(c, id)
		if err != nil {
			c.AbortWithStatusJSON(404, gin.H{"error": "node not found"})
			return
		}
		authz := c.GetHeader("Authorization")
		t := strings.TrimPrefix(authz, "Bearer ")
		if !auth.CheckNodeToken(t, node.Token) {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
