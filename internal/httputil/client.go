// Package httputil 提供管理端与 agent 共用的 HTTP 客户端构造，
// 统一管理 TLS 校验开关（config.AgentTLSVerify）。
package httputil

import (
	"crypto/tls"
	"net/http"
	"time"

	"nodepilot/internal/config"
)

// AgentClient 返回带超时的 HTTP 客户端。
// 当 config.AgentTLSVerify 为 false（默认，MVP 兼容）时跳过对端证书校验；
// 生产环境建议设为 true，此时使用系统根证书池做正常校验。
func AgentClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{}
	if !config.AgentTLSVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: timeout, Transport: transport}
}
