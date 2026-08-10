package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"

	"nodepilot/internal/config"
)

// jwtSecret 来自 config.Init（环境变量或持久化密钥文件），不再硬编码。

// HashPassword bcrypt 哈希管理员密码
func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

// CheckPassword 校验管理员密码
func CheckPassword(pw, hash string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// GenToken 生成 32 字节随机 hex（用于节点 token）
func GenToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 极不可能失败；失败则退回时间熵
		return hex.EncodeToString([]byte(time.Now().String()))
	}
	return hex.EncodeToString(b)
}

// TokenHash 返回 token 的 sha256 hex（可用于日志/校验，避免明文）
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// CheckNodeToken 校验节点 Bearer token：
// 对传入的 bearer 取 sha256 hex，与已存储的哈希（expectedHash）做常量时间比较，
// 避免时序攻击，且数据库侧不再保存明文 token。
func CheckNodeToken(bearer, expectedHash string) bool {
	if expectedHash == "" || bearer == "" {
		return false
	}
	h := sha256.Sum256([]byte(bearer))
	hh := hex.EncodeToString(h[:])
	return subtle.ConstantTimeCompare([]byte(hh), []byte(expectedHash)) == 1
}

// IssueJWT 签发管理员 JWT（24h）
func IssueJWT(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(config.JWTSecret)
}

// ParseJWT 校验并返回 claims
func ParseJWT(tokenStr string) (jwt.MapClaims, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return config.JWTSecret, nil
	})
	if err != nil {
		return nil, err
	}
	return t.Claims.(jwt.MapClaims), nil
}
