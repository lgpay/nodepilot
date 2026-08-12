package configgen

import (
	"encoding/json"
	"testing"

	"nodepilot/internal/model"
)

func TestBuildXrayConfig(t *testing.T) {
	node := model.Node{ID: 1}
	inbounds := []model.Inbound{
		{ID: 1, NodeID: 1, Protocol: "vmess", Port: 10001, Transport: "tcp", Enabled: true},
		{ID: 2, NodeID: 1, Protocol: "vless", Port: 10002, Transport: "ws", Enabled: true},
		{ID: 3, NodeID: 1, Protocol: "trojan", Port: 10003, Transport: "tcp", TLSEnabled: true, TLSCertID: 1, Enabled: true},
		{ID: 4, NodeID: 1, Protocol: "vmess", Port: 10004, Transport: "tcp", Enabled: false}, // 禁用应被排除
	}
	clients := map[uint][]model.Client{
		1: {{UUID: "11111111-1111-1111-1111-111111111111", Enabled: true}},
		2: {{UUID: "22222222-2222-2222-2222-222222222222", Enabled: true}},
		3: {{UUID: "33333333-3333-3333-3333-333333333333", Enabled: true}},
	}
	s, err := BuildXrayConfig(node, inbounds, clients)
	if err != nil {
		t.Fatalf("BuildXrayConfig: %v", err)
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal([]byte(s), &cfg); err != nil {
		t.Fatalf("输出不是合法 JSON: %v", err)
	}
	// 流量统计依赖
	if cfg["stats"] == nil || cfg["api"] == nil || cfg["policy"] == nil {
		t.Error("缺少 stats/api/policy（流量统计）配置")
	}
	inList, ok := cfg["inbounds"].([]interface{})
	if !ok || len(inList) != 3 {
		t.Fatalf("inbounds 数量错误（应排除禁用入站）: %v", cfg["inbounds"])
	}
	// 逐个校验协议与端口
	byTag := map[string]map[string]interface{}{}
	for _, v := range inList {
		ib, _ := v.(map[string]interface{})
		byTag[ib["tag"].(string)] = ib
	}
	vmess := byTag["in-1"]
	if vmess == nil || vmess["protocol"] != "vmess" || vmess["port"].(float64) != 10001 {
		t.Errorf("vmess 入站错误: %v", vmess)
	}
	vless := byTag["in-2"]
	if vless == nil {
		t.Fatalf("缺少 vless 入站")
	}
	settings, _ := vless["settings"].(map[string]interface{})
	if settings["decryption"] != "none" {
		t.Errorf("vless 应声明 decryption=none，got %v", settings["decryption"])
	}
	stream, _ := vless["streamSettings"].(map[string]interface{})
	if stream["network"] != "ws" {
		t.Errorf("vless 传输应为 ws: %v", stream["network"])
	}
	trojan := byTag["in-3"]
	if trojan == nil {
		t.Fatalf("缺少 trojan 入站")
	}
	trojanSettings, _ := trojan["settings"].(map[string]interface{})
	cls, _ := trojanSettings["clients"].([]interface{})
	if len(cls) != 1 {
		t.Fatalf("trojan clients 数量错误")
	}
	c0, _ := cls[0].(map[string]interface{})
	if c0["password"] == nil {
		t.Error("trojan client 应使用 password 字段")
	}
}

func TestGenUUIDFormat(t *testing.T) {
	u := GenUUID()
	if len(u) != 36 {
		t.Fatalf("UUID 长度错误: %s", u)
	}
	// RFC4122 v4：第 15 位为 4
	if u[14] != '4' {
		t.Errorf("非 v4 UUID: %s", u)
	}
}
