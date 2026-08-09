package server

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/model"
	"nodepilot/internal/secret"
	"nodepilot/internal/store"
)

const (
	ctrlCertDir = "/opt/nodepilot/certs" // 管理端 lego 签发产物目录
	legoBin     = "/usr/local/bin/lego"
	agentCertFile = "/opt/nodepilot-agent/certs/fullchain.pem"
	agentKeyFile  = "/opt/nodepilot-agent/certs/privkey.pem"
	agentCaFile   = "/opt/nodepilot-agent/certs/ca.pem"
)

// ListCerts 列出全局证书
func ListCerts(c *gin.Context) {
	var certs []model.Certificate
	store.DB.Order("id desc").Find(&certs)
	c.JSON(200, certs)
}

// CreateCert 新建证书并发起签发 + 分发
func CreateCert(c *gin.Context) {
	var body struct {
		Domain     string `json:"domain"`
		CFEmail    string `json:"cf_email"`
		CFAPIToken string `json:"cf_api_token"`
		AutoRenew  *bool  `json:"auto_renew"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if body.Domain == "" || body.CFAPIToken == "" {
		c.JSON(400, gin.H{"error": "domain and cf_api_token required"})
		return
	}
	autoRenew := true
	if body.AutoRenew != nil {
		autoRenew = *body.AutoRenew
	}
	tokenEnc, err := secret.Encrypt(body.CFAPIToken)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	cert := model.Certificate{
		Domain:     body.Domain,
		CFEmail:    body.CFEmail,
		CFTokenEnc: tokenEnc,
		AutoRenew:  autoRenew,
		Status:     "pending",
		CertPath:   agentCertFile,
		KeyPath:    agentKeyFile,
		CAPath:     agentCaFile,
	}
	if err := store.DB.Create(&cert).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	exp, err := issueCert(body.Domain, body.CFEmail, body.CFAPIToken)
	if err != nil {
		cert.Status = "failed"
		cert.LastError = err.Error()
		store.DB.Model(&cert).Updates(map[string]interface{}{"status": "failed", "last_error": err.Error()})
		c.JSON(502, gin.H{"error": "签发失败: " + err.Error(), "id": cert.ID, "status": "failed"})
		return
	}
	store.DB.Model(&cert).Updates(map[string]interface{}{"status": "issued", "expires_at": exp, "last_error": ""})
	cert.ExpiresAt = exp
	cert.Status = "issued"
	if err := distributeCert(cert); err != nil {
		log.Printf("[cert] distribute warning: %v", err)
	}
	c.JSON(200, cert)
}

// GetCert 证书详情
func GetCert(c *gin.Context) {
	id := c.Param("id")
	var cert model.Certificate
	if err := store.DB.First(&cert, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, cert)
}

// DeleteCert 删除证书记录
func DeleteCert(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Certificate{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// RenewCert 重新签发并分发
func RenewCert(c *gin.Context) {
	id := c.Param("id")
	var cert model.Certificate
	if err := store.DB.First(&cert, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	token, err := secret.Decrypt(cert.CFTokenEnc)
	if err != nil || token == "" {
		c.JSON(500, gin.H{"error": "解密 CF token 失败"})
		return
	}
	exp, err := issueCert(cert.Domain, cert.CFEmail, token)
	if err != nil {
		store.DB.Model(&cert).Updates(map[string]interface{}{"status": "failed", "last_error": err.Error()})
		c.JSON(502, gin.H{"error": "签发失败: " + err.Error()})
		return
	}
	store.DB.Model(&cert).Updates(map[string]interface{}{"status": "issued", "expires_at": exp, "last_error": ""})
	if err := distributeCert(cert); err != nil {
		log.Printf("[cert] distribute warning: %v", err)
	}
	c.JSON(200, gin.H{"ok": true, "expires_at": exp})
}

// DistributeCert 仅把已有证书重分发到所有节点（新增节点后有用）
func DistributeCert(c *gin.Context) {
	id := c.Param("id")
	var cert model.Certificate
	if err := store.DB.First(&cert, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if err := distributeCert(cert); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// issueCert 调用 lego 走 Cloudflare DNS-01 签发/续签泛域名证书，返回到期时间
func issueCert(domain, cfEmail, cfToken string) (time.Time, error) {
	if _, err := exec.LookPath(legoBin); err != nil {
		return time.Time{}, fmt.Errorf("lego 未安装: %s（管理端需安装 lego）", legoBin)
	}
	base := filepath.Join(ctrlCertDir, "certificates", sanitize(domain)+".crt")
	// lego v5：始终用 `run`——证书不存在时签发，已存在且临近到期时自动续签；
	// 子命令参数（--dns/--domains/--key-type/--path/--accept-tos）必须放在 run 之后。
	// 邮箱无需传递（API Token 方式），lego 缺省使用 noemail@example.com 注册账户。
	args := []string{"run", "--dns", "cloudflare", "--domains", domain, "--key-type", "rsa2048", "--path", ctrlCertDir, "--accept-tos"}
	cmd := exec.Command(legoBin, args...)
	cmd.Env = append(os.Environ(), "CLOUDFLARE_DNS_API_TOKEN="+cfToken)
	cmd.Stdout = log.Writer()
	cmd.Stderr = log.Writer()
	if err := cmd.Run(); err != nil {
		return time.Time{}, fmt.Errorf("lego 执行失败: %v", err)
	}
	data, err := os.ReadFile(base)
	if err != nil {
		return time.Time{}, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return time.Time{}, fmt.Errorf("无法解析证书 PEM")
	}
	x, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return time.Time{}, err
	}
	return x.NotAfter, nil
}

// distributeCert 读取管理端签发产物，推送到各在线节点并触发配置重载
func distributeCert(cert model.Certificate) error {
	base := filepath.Join(ctrlCertDir, "certificates", sanitize(cert.Domain))
	certPEM, err1 := os.ReadFile(base + ".crt")
	keyPEM, err2 := os.ReadFile(base + ".key")
	if err1 != nil || err2 != nil {
		return fmt.Errorf("读取签发产物失败: crt=%v key=%v", err1, err2)
	}
	caPEM, _ := os.ReadFile(base + ".issuer.crt")
	var nodes []model.Node
	store.DB.Where("enabled = ?", true).Find(&nodes)
	var lastErr error
	for _, n := range nodes {
		_, err := agentPut(n, "/agent/v1/cert", map[string]string{
			"cert_pem": string(certPEM),
			"key_pem":  string(keyPEM),
			"ca_pem":   string(caPEM),
		})
		if err != nil {
			log.Printf("[cert] 分发到节点 %d 失败: %v", n.ID, err)
			lastErr = err
			continue
		}
		if _, err := syncNode(n); err != nil {
			log.Printf("[cert] 节点 %d 配置重载失败: %v", n.ID, err)
		}
	}
	return lastErr
}

// sanitize 与 lego 的证书文件名处理保持一致（通配符 * -> _）
// 例：*.seew.tk -> _.seew.tk
func sanitize(domain string) string {
	return strings.ReplaceAll(domain, "*", "_")
}

// StartCertRenewScheduler 定时检查临近过期的证书并自动续签+重分发
func StartCertRenewScheduler() {
	go func() {
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			renewDueCerts()
		}
	}()
}

func renewDueCerts() {
	var certs []model.Certificate
	store.DB.Where("auto_renew = ? AND status = ?", true, "issued").Find(&certs)
	threshold := time.Now().Add(30 * 24 * time.Hour)
	for _, cert := range certs {
		if !cert.ExpiresAt.IsZero() && cert.ExpiresAt.Before(threshold) {
			token, err := secret.Decrypt(cert.CFTokenEnc)
			if err != nil || token == "" {
				log.Printf("[cert] 节点证书 #%d 续签跳过：token 解密失败", cert.ID)
				continue
			}
			exp, err := issueCert(cert.Domain, cert.CFEmail, token)
			if err != nil {
				log.Printf("[cert] 证书 #%d 续签失败: %v", cert.ID, err)
				store.DB.Model(&cert).Updates(map[string]interface{}{"status": "failed", "last_error": err.Error()})
				continue
			}
			store.DB.Model(&cert).Updates(map[string]interface{}{"status": "issued", "expires_at": exp, "last_error": ""})
			if err := distributeCert(cert); err != nil {
				log.Printf("[cert] 证书 #%d 续签后分发警告: %v", cert.ID, err)
			}
			log.Printf("[cert] 证书 #%d 续签成功，新到期 %s", cert.ID, exp.Format(time.RFC3339))
		}
	}
}
