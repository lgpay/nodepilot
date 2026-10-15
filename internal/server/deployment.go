package server

import (
	"log"
	"os"
	"path/filepath"

	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// inboundDeployment describes the asynchronous deployment started after an
// inbound is saved. BaseVersion is captured before the worker starts so the
// UI can distinguish this deployment from older config versions.
type inboundDeployment struct {
	Attempted   bool   `json:"attempted"`
	Status      string `json:"status,omitempty"`
	Version     int    `json:"version,omitempty"`
	BaseVersion int    `json:"base_version,omitempty"`
	Error       string `json:"error,omitempty"`
}

func afterInboundSave(in model.Inbound) inboundDeployment {
	security := in.Security
	if security == "" {
		if in.TLSEnabled {
			security = model.SecurityTLS
		} else {
			security = model.SecurityNone
		}
	}
	if security != model.SecurityTLS && security != model.SecurityReality {
		return inboundDeployment{}
	}
	var maxVersion int
	store.DB.Model(&model.ConfigVersion{}).
		Where("node_id = ?", in.NodeID).
		Select("COALESCE(MAX(version),0)").Scan(&maxVersion)
	go func(in model.Inbound) {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[inbound] 异步下发 panic 恢复: %v", r)
			}
		}()
		var node model.Node
		if err := store.DB.First(&node, in.NodeID).Error; err != nil {
			log.Printf("[inbound] 节点 #%d 不存在，跳过配置下发", in.NodeID)
			return
		}
		if security == model.SecurityTLS && in.TLSCertID > 0 {
			pushCertToNode(node, in.TLSCertID)
		}
		version, err := syncNode(node)
		if err != nil {
			log.Printf("[inbound] 节点 #%d 异步配置下发失败: %v", node.ID, err)
			return
		}
		log.Printf("[inbound] 节点 #%d 异步配置下发完成 (version=%d)", node.ID, version)
	}(in)
	return inboundDeployment{Attempted: true, Status: "queued", BaseVersion: maxVersion}
}

func pushCertToNode(node model.Node, certID uint) {
	if certID == 0 {
		return
	}
	var cert model.Certificate
	if err := store.DB.First(&cert, certID).Error; err != nil {
		log.Printf("[inbound] 证书 #%d 不存在，跳过对节点 %d 的分发", certID, node.ID)
		return
	}
	if cert.Status != "issued" {
		log.Printf("[inbound] 证书 #%d 状态 %s，跳过对节点 %d 的分发", certID, cert.Status, node.ID)
		return
	}
	base := filepath.Join(ctrlCertDir, "certificates", sanitize(cert.Domain))
	certPEM, err1 := os.ReadFile(base + ".crt")
	keyPEM, err2 := os.ReadFile(base + ".key")
	if err1 != nil || err2 != nil {
		log.Printf("[inbound] 读取证书产物失败(crt=%v key=%v)，跳过对节点 %d 的分发", err1, err2, node.ID)
		return
	}
	caPEM, _ := os.ReadFile(base + ".issuer.crt")
	if _, err := agentPut(node, "/agent/v1/cert", map[string]string{
		"cert_pem": string(certPEM),
		"key_pem":  string(keyPEM),
		"ca_pem":   string(caPEM),
	}); err != nil {
		log.Printf("[inbound] 证书分发到节点 %d 失败: %v", node.ID, err)
		return
	}
	log.Printf("[inbound] 证书 #%d 已分发到节点 %d", certID, node.ID)
}
