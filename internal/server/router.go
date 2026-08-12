package server

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// NewRouter 构建管理端 HTTP 路由（/api/v1）
// webDir 非空时在该目录下提供 index.html 作为 Web 管理界面（运行目录与项目目录隔离）
func NewRouter(webDir string) *gin.Engine {
	r := gin.Default()
	v1 := r.Group("/api/v1")

	// 公开：管理员登录（加 IP 速率限制，防暴力破解）
	v1.POST("/auth/login", LoginRateLimit(20, time.Minute), LoginHandler)
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
		authed.GET("/nodes/:id/traffic", NodeTraffic)
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

		// IP 归属查询（自动识别节点区域）
		authed.GET("/geo", GeoLookup)

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

	// 自托管 ACL4SSR 规则镜像（公开，客户端 rule-provider / RULE-SET 引用）
	v1.GET("/rules/:name", GetRuleFile)

	// Web 管理界面（单页 index.html）
	if webDir != "" {
		if idx := filepath.Join(webDir, "index.html"); fileExists(idx) {
			r.StaticFile("/", idx)
		}
		// favicon：从 webDir 提供；浏览器自动请求 /favicon.ico，双别名兜底
		if fp := filepath.Join(webDir, "favicon.png"); fileExists(fp) {
			r.StaticFile("/favicon.png", fp)
			r.StaticFile("/favicon.ico", fp)
		}
	}

	return r
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// AuthMiddleware 校验管理员 JWT，并在 MustChangePwd 时拦截除修改密码外的所有接口。
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authz := c.GetHeader("Authorization")
		t := strings.TrimPrefix(authz, "Bearer ")
		if t == "" {
			c.AbortWithStatusJSON(401, gin.H{"error": "missing token"})
			return
		}
		claims, err := auth.ParseJWT(t)
		if err != nil {
			c.AbortWithStatusJSON(401, gin.H{"error": "invalid token"})
			return
		}
		// 首次启动随机密码未修改前，仅放行修改密码接口（强制改密）
		if strings.HasSuffix(c.FullPath(), "/auth/change-password") {
			c.Next()
			return
		}
		if username, _ := claims["sub"].(string); username != "" {
			var admin model.Admin
			if store.DB.Where("username = ?", username).First(&admin).Error == nil {
				if admin.MustChangePwd {
					c.AbortWithStatusJSON(403, gin.H{"error": "please change password first", "must_change_pwd": true})
					return
				}
				// JWT 版本校验：改密后 Admin.TokenVersion 递增，旧 token（ver 缺失或不匹配）立即失效
				ver, ok := claims["ver"].(float64)
				if !ok || int(ver) != admin.TokenVersion {
					c.AbortWithStatusJSON(401, gin.H{"error": "token revoked, please login again"})
					return
				}
			}
		}
		c.Next()
	}
}

// loginLimit 基于 IP 的固定窗口速率限制（无第三方依赖）。
type loginLimit struct {
	mu       sync.Mutex
	hits     map[string]int
	window   time.Time
	maxHits  int
	interval time.Duration
}

// LoginRateLimit 限制每个 IP 在 interval 内最多 max 次登录尝试。
func LoginRateLimit(max int, interval time.Duration) gin.HandlerFunc {
	lim := &loginLimit{
		hits:     map[string]int{},
		window:   time.Now(),
		maxHits:  max,
		interval: interval,
	}
	return func(c *gin.Context) {
		ip := c.ClientIP()
		lim.mu.Lock()
		// 窗口到期则重置
		if time.Since(lim.window) >= lim.interval {
			lim.hits = map[string]int{}
			lim.window = time.Now()
		}
		n := lim.hits[ip]
		if n >= lim.maxHits {
			lim.mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "too many attempts, try later"})
			return
		}
		lim.hits[ip] = n + 1
		lim.mu.Unlock()
		c.Next()
	}
}

// NodeTokenMiddleware 校验节点 Bearer token（比对 sha256(bearer) 与存储的 TokenHash）
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
		if !auth.CheckNodeToken(t, node.TokenHash) {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}
