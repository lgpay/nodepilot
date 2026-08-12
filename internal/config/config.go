// Package config 集中管理运行时密钥与开关。
//
// 安全说明：
//   - JWT 签名密钥与 AES 主密钥不再硬编码。优先从环境变量读取：
//     NP_JWT_SECRET / NP_MASTER_KEY。
//   - 若环境变量为空，则在数据库同目录下生成随机密钥并持久化到隐藏文件
//     （权限 0600），保证同一安装跨重启一致，且不再是可预测的 dev 占位串。
//   - 管理端<->agent 的 TLS 校验开关由 NP_AGENT_TLS_VERIFY 控制（默认关闭，
//     保持 MVP 兼容；生产建议设为 true）。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// JWTSecret JWT HS256 签名密钥。
var JWTSecret []byte

// MasterKey AES-256 主密钥（32 字节）。
var MasterKey []byte

// AgentTLSVerify 是否校验 agent 端 TLS 证书（默认 false = InsecureSkipVerify）。
var AgentTLSVerify bool

// Init 在 store.Init 之前调用，加载/生成密钥与开关。
// dbPath 仅用于推导密钥文件的存放目录（与数据库同在一级目录下）。
func Init(dbPath string) {
	JWTSecret = loadOrGenerate("NP_JWT_SECRET", dbPath, ".nodepilot_jwt_secret")
	MasterKey = deriveMasterKey(loadOrGenerate("NP_MASTER_KEY", dbPath, ".nodepilot_master_key"))

	AgentTLSVerify = strings.EqualFold(os.Getenv("NP_AGENT_TLS_VERIFY"), "true")
}

// InitAgent 仅初始化 agent 端关心的 TLS 校验开关，不生成/持久化任何密钥文件
// （agent 不使用 JWT/AES 密钥）。控制端二进制应改用 Init。
func InitAgent() {
	AgentTLSVerify = strings.EqualFold(os.Getenv("NP_AGENT_TLS_VERIFY"), "true")
}

// loadOrGenerate 优先读环境变量；否则从密钥文件读取，缺失则生成随机 32 字节 hex 并落盘。
func loadOrGenerate(envKey, dbPath, fileName string) []byte {
	if v := os.Getenv(envKey); v != "" {
		return []byte(v)
	}
	dir := "."
	if dbPath != "" {
		dir = filepath.Dir(dbPath)
	}
	path := filepath.Join(dir, fileName)
	if b, err := os.ReadFile(path); err == nil {
		if s := strings.TrimSpace(string(b)); s != "" {
			return []byte(s)
		}
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// 密码学随机源不可用是致命错误，不能静默降级到可预测熵
		panic("crypto/rand unavailable: " + err.Error())
	}
	s := hex.EncodeToString(buf)
	_ = os.WriteFile(path, []byte(s), 0600)
	return []byte(s)
}

// deriveMasterKey 统一为 32 字节（AES-256）：截断或补零。
func deriveMasterKey(env []byte) []byte {
	k := make([]byte, 32)
	copy(k, env)
	return k
}
