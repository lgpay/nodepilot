// Package secret 提供基于 AES-GCM 的对称加密，用于加密存储敏感字段（如 Cloudflare API Token）。
// master key 取自环境变量 NP_MASTER_KEY，缺省为开发占位（生产务必通过环境变量注入）。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"os"
)

var key = deriveKey(os.Getenv("NP_MASTER_KEY"))

func deriveKey(env string) []byte {
	if env == "" {
		env = "nodepilot-dev-secret-change-me"
	}
	// 统一为 32 字节（AES-256）：截断或补零
	k := make([]byte, 32)
	copy(k, []byte(env))
	return k
}

// Encrypt 明文加密为 base64 密文
func Encrypt(plaintext string) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ct := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ct), nil
}

// Decrypt base64 密文解密为明文
func Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	ns := gcm.NonceSize()
	if len(data) < ns {
		return "", errors.New("ciphertext too short")
	}
	nonce, ct := data[:ns], data[ns:]
	pt, err := gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", err
	}
	return string(pt), nil
}
