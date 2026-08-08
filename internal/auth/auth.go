package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
)

// jwtSecret MVP 写死，生产应来自配置/环境变量
var jwtSecret = []byte("nodepilot-dev-secret-change-me")

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

// CheckNodeToken 校验节点上报/下发的 Bearer token：
// 比较 sha256(bearer) 与 sha256(expected)，常量时间避免时序攻击
func CheckNodeToken(bearer, expected string) bool {
	h1 := sha256.Sum256([]byte(bearer))
	h2 := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(h1[:], h2[:]) == 1
}

// IssueJWT 签发管理员 JWT（24h）
func IssueJWT(username string) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(24 * time.Hour).Unix(),
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(jwtSecret)
}

// ParseJWT 校验并返回 claims
func ParseJWT(tokenStr string) (jwt.MapClaims, error) {
	t, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}
	return t.Claims.(jwt.MapClaims), nil
}
