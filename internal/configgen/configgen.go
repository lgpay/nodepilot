package configgen

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"nodepilot/internal/model"
)

// BuildXrayConfig 将节点下的入站与用户拼装为 xray config.json 字符串
func BuildXrayConfig(node model.Node, inbounds []model.Inbound, clientsByInbound map[uint][]model.Client) (string, error) {
	cfg := map[string]interface{}{
		"log": map[string]interface{}{"loglevel": "warning"},
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
			clientList = append(clientList, map[string]interface{}{
				"id":    c.UUID,
				"level": 0,
				"email": c.Email,
			})
		}

		network := in.Transport
		if network == "" {
			network = "tcp"
		}
		stream := map[string]interface{}{"network": network}
		switch network {
		case "ws":
			stream["wsSettings"] = map[string]interface{}{"path": "/v2ray"}
		case "grpc":
			stream["grpcSettings"] = map[string]interface{}{"serviceName": "xray"}
		}
		if in.TLSEnabled {
			stream["security"] = "tls"
			// MVP：cert_id 解析为证书路径留待 P2 证书管理；此处占位
			stream["tlsSettings"] = map[string]interface{}{
				"certificates": []map[string]interface{}{
					{"certificateFile": "/root/cert/fullchain.pem", "keyFile": "/root/cert/privkey.pem"},
				},
			}
		} else {
			stream["security"] = "none"
		}

		ib := map[string]interface{}{
			"listen":         "0.0.0.0",
			"port":           in.Port,
			"protocol":       in.Protocol,
			"settings":       map[string]interface{}{"clients": clientList},
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

// GenUUID 生成 RFC4122 v4 UUID（用于 vmess 等客户端 id）
func GenUUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
