package server

import (
	"log"
	"os"
	"path/filepath"
	"sync"

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

type deploymentJob struct {
	in       model.Inbound
	security string
}

const deploymentQueueSize = 64

var (
	deploymentQueue    = make(chan deploymentJob, deploymentQueueSize)
	deploymentWorkerWg sync.WaitGroup
)

func init() {
	deploymentWorkerWg.Add(1)
	go runDeploymentWorker()
}

func enqueueDeployment(in model.Inbound, security string) bool {
	select {
	case deploymentQueue <- deploymentJob{in: in, security: security}:
		return true
	default:
		return false
	}
}

func runDeploymentWorker() {
	defer deploymentWorkerWg.Done()
	for job := range deploymentQueue {
		executeDeployment(job)
	}
}

func executeDeployment(job deploymentJob) {
	in := job.in
	security := job.security
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[deployment] worker panic recovered: %v", r)
		}
	}()
	var node model.Node
	if err := store.DB.First(&node, in.NodeID).Error; err != nil {
		log.Printf("[deployment] node #%d not found, skipping", in.NodeID)
		return
	}
	if security == model.SecurityTLS && in.TLSCertID > 0 {
		pushCertToNode(node, in.TLSCertID)
	}
	version, err := syncNode(node)
	if err != nil {
		log.Printf("[deployment] node #%d sync failed: %v", node.ID, err)
		return
	}
	log.Printf("[deployment] node #%d sync completed (version=%d)", node.ID, version)
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
	if !enqueueDeployment(in, security) {
		return inboundDeployment{Attempted: true, Status: "failed", Error: "deployment queue is full"}
	}
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
