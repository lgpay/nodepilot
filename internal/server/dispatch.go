package server

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/configgen"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// SyncNode 触发向节点 agent 下发配置（推模式）
func SyncNode(c *gin.Context) {
	id := c.Param("id")
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	if !node.Enabled {
		c.JSON(400, gin.H{"error": "node disabled"})
		return
	}
	version, err := syncNode(node)
	if err != nil {
		c.JSON(502, gin.H{"error": "dispatch failed: " + err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true, "version": version})
}

// syncNode 生成配置并下发到节点 agent，返回新配置版本号（handler 与探测调度器共用）
func syncNode(node model.Node) (int, error) {
	var inbounds []model.Inbound
	store.DB.Where("node_id = ?", node.ID).Find(&inbounds)
	clientsByInbound := map[uint][]model.Client{}
	for _, in := range inbounds {
		var cls []model.Client
		store.DB.Where("inbound_id = ?", in.ID).Find(&cls)
		clientsByInbound[in.ID] = cls
	}

	jsonStr, err := configgen.BuildXrayConfig(node, inbounds, clientsByInbound)
	if err != nil {
		return 0, err
	}

	var maxVer int
	store.DB.Model(&model.ConfigVersion{}).
		Where("node_id = ?", node.ID).
		Select("COALESCE(MAX(version),0)").Scan(&maxVer)
	version := maxVer + 1

	payload, _ := json.Marshal(map[string]interface{}{
		"version":     version,
		"xray_config": json.RawMessage(jsonStr),
	})

	url := agentURL(node.Address) + "/agent/v1/config"
	req, err := http.NewRequest(http.MethodPut, url, bytes.NewReader(payload))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+node.Token)

	client := &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // MVP：agent 自签/HTTP，生产需校验证书
		},
	}
	resp, err := client.Do(req)
	cv := model.ConfigVersion{
		NodeID:      node.ID,
		Version:     version,
		ContentJSON: jsonStr,
		AppliedAt:   time.Now(),
	}
	if err != nil {
		cv.Status = "failed"
		cv.Error = err.Error()
		store.DB.Create(&cv)
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		cv.Status = "failed"
		cv.Error = string(b)
		store.DB.Create(&cv)
		return 0, fmt.Errorf("agent rejected: %s", string(b))
	}
	cv.Status = "applied"
	store.DB.Create(&cv)
	return version, nil
}

// ListConfigVersions 查看节点的配置下发历史
func ListConfigVersions(c *gin.Context) {
	id := c.Param("id")
	var versions []model.ConfigVersion
	store.DB.Where("node_id = ?", id).Order("version desc").Find(&versions)
	c.JSON(200, versions)
}

// agentURL 规范化节点 agent 地址：已有 scheme 则用，否则补 http://（MVP 演示用）
func agentURL(address string) string {
	if len(address) >= 4 && (address[:4] == "http") {
		return address
	}
	return "http://" + address
}
