package server

import (
	"strings"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/auth"
)

// NewRouter 构建管理端 HTTP 路由（/api/v1）
func NewRouter() *gin.Engine {
	r := gin.Default()
	v1 := r.Group("/api/v1")

	// 公开：管理员登录
	v1.POST("/auth/login", LoginHandler)
	v1.POST("/auth/logout", AuthMiddleware(), LogoutHandler)

	// 管理员受保护接口
	authed := v1.Group("")
	authed.Use(AuthMiddleware())
	{
		authed.GET("/nodes", ListNodes)
		authed.POST("/nodes", CreateNode)
		authed.GET("/nodes/:id", GetNode)
		authed.PATCH("/nodes/:id", UpdateNode)
		authed.DELETE("/nodes/:id", DeleteNode)

		authed.GET("/nodes/:id/inbounds", ListInbounds)
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

	return r
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
