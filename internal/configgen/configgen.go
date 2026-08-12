package configgen

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"

	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// xrayStatsAddr 控制面生成配置时固定的 xray stats API 监听地址；
// agent 端流量采集器须使用同一地址（internal/agent/traffic.go 的 collectTraffic）。
const xrayStatsAddr = "127.0.0.1:10085"

// agentCertFile / agentKeyFile 与 agent 侧落盘路径（internal/agent/cert.go）保持一致，
// 入站启用 TLS 但未关联证书时的兜底路径。
const (
	agentCertFile = "/opt/nodepilot-agent/certs/fullchain.pem"
	agentKeyFile  = "/opt/nodepilot-agent/certs/privkey.pem"
)

// BuildXrayConfig 将节点下的入站与用户拼装为 xray config.json 字符串
func BuildXrayConfig(node model.Node, inbounds []model.Inbound, clientsByInbound map[uint][]model.Client) (string, error) {
	cfg := map[string]interface{}{
		"log": map[string]interface{}{"loglevel": "warning"},
		// 流量统计：stats 启用计数；api 暴露 StatsService 供 agent 查询；
		// policy 开启 per-user 上下行计数（计数键为 client 的 UUID）。
		"stats": map[string]interface{}{},
		"api": map[string]interface{}{
			"tag":      "api",
			"listen":   xrayStatsAddr,
			"services": []string{"StatsService"},
		},
		"policy": map[string]interface{}{
			"levels": map[string]interface{}{
				"0": map[string]interface{}{
					"statsUserUplink":   true,
					"statsUserDownlink": true,
				},
			},
			"system": map[string]interface{}{
				"statsInboundUplink":   true,
				"statsInboundDownlink": true,
			},
		},
		"outbounds": []map[string]interface{}{
			{"protocol": "freedom", "tag": "direct"},
		},
		"routing": map[string]interface{}{"rules": []interface{}{}},
	}

	inboundList := []map[string]interface{}{}
	for _, in := range inbounds {
		if !in.Enabled {
			continue
		}
		clientList := []map[string]interface{}{}
		for _, c := range clientsByInbound[in.ID] {
			if !c.Enabled {
				continue
			}
			clientList = append(clientList, buildClient(in.Protocol, c.UUID))
		}

		network := in.Transport
		if network == "" {
			network = "tcp"
		}
		// 解析入站保存的 stream_settings（前端传 {"wsPath":"/x"} 或 xray 风格 {"wsSettings":{"path":"/x"}}）
		var ss struct {
			WsPath     string `json:"wsPath"`
			WsSettings struct {
				Path string `json:"path"`
			} `json:"wsSettings"`
			GrpcSettings struct {
				ServiceName string `json:"serviceName"`
			} `json:"grpcSettings"`
		}
		_ = json.Unmarshal([]byte(in.StreamSettings), &ss)
		wsPath := ss.WsSettings.Path
		if wsPath == "" {
			wsPath = ss.WsPath
		}
		if wsPath == "" {
			wsPath = "/v2ray"
		}
		grpcSvc := ss.GrpcSettings.ServiceName
		if grpcSvc == "" {
			grpcSvc = "xray"
		}
		stream := map[string]interface{}{"network": network}
		switch network {
		case "ws":
			stream["wsSettings"] = map[string]interface{}{"path": wsPath}
		case "grpc":
			stream["grpcSettings"] = map[string]interface{}{"serviceName": grpcSvc}
		}
		if in.TLSEnabled {
			stream["security"] = "tls"
			// 默认路径与 agent 实际落盘路径（internal/agent/cert.go 的 ReceiveCert）保持一致
			certFile, keyFile := agentCertFile, agentKeyFile
			if in.TLSCertID > 0 && store.DB != nil {
				var cert model.Certificate
				if store.DB.First(&cert, in.TLSCertID).Error == nil && cert.CertPath != "" {
					certFile, keyFile = cert.CertPath, cert.KeyPath
				}
			}
			stream["tlsSettings"] = map[string]interface{}{
				"certificates": []map[string]interface{}{
					{"certificateFile": certFile, "keyFile": keyFile},
				},
			}
		} else {
			stream["security"] = "none"
		}

		// xray 26.x 要求 VLESS 入站 settings 显式声明 decryption（服务端不解密，填 none）。
		settings := map[string]interface{}{"clients": clientList}
		if in.Protocol == "vless" {
			settings["decryption"] = "none"
		}
		ib := map[string]interface{}{
			"listen":         "0.0.0.0",
			"port":           in.Port,
			"protocol":       in.Protocol,
			"settings":       settings,
			"streamSettings": stream,
			"tag":            "in-" + strconv.FormatUint(uint64(in.ID), 10),
		}
		inboundList = append(inboundList, ib)
	}
	cfg["inbounds"] = inboundList

	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	_ = node
	return string(b), nil
}

// buildClient 按协议生成 xray 客户端条目。
// vmess / vless 用 id（UUID）作为身份标识；trojan 用 password（复用 UUID，随机串即合法口令）。
// email 统一取 UUID，作为 per-user 流量统计键（见 BuildXrayConfig 注释），改名 alias 不影响历史流量归属。
func buildClient(protocol, uuid string) map[string]interface{} {
	m := map[string]interface{}{
		"level": 0,
		"email": uuid,
	}
	if protocol == "trojan" {
		m["password"] = uuid
	} else {
		m["id"] = uuid
	}
	return m
}

// GenUUID 生成 RFC4122 v4 UUID（用于 vmess 等客户端 id）
func GenUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
