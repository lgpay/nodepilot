package agent

import (
	"log"
	"os"
	"path/filepath"
)

var (
	certDir     = "/opt/nodepilot-agent/certs"
	certFile    = "fullchain.pem"
	keyFile     = "privkey.pem"
	caFile      = "ca.pem"
)

// SetCertDir 设置 agent 本地证书目录
func SetCertDir(dir string) {
	if dir != "" {
		certDir = dir
	}
}

// ReceiveCert 将管理端下发的证书内容写入本地文件（证书私钥仅存节点本地）
func ReceiveCert(certPEM, keyPEM, caPEM string) error {
	if err := os.MkdirAll(certDir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, certFile), []byte(certPEM), 0600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(certDir, keyFile), []byte(keyPEM), 0600); err != nil {
		return err
	}
	if caPEM != "" {
		if err := os.WriteFile(filepath.Join(certDir, caFile), []byte(caPEM), 0600); err != nil {
			return err
		}
	}
	log.Printf("[agent] certificate written to %s", certDir)
	return nil
}

// CertPaths 返回 agent 本地证书文件路径（供状态展示）
func CertPaths() map[string]string {
	return map[string]string{
		"cert": filepath.Join(certDir, certFile),
		"key":  filepath.Join(certDir, keyFile),
		"ca":   filepath.Join(certDir, caFile),
	}
}
