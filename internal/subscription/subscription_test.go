package subscription

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestBuildVMess(t *testing.T) {
	items := []ExportItem{{Host: "1.2.3.4", Port: 443, Protocol: "vmess", Transport: "tcp", UUID: "u1", Alias: "test"}}
	s, err := BuildVMess(items)
	if err != nil {
		t.Fatal(err)
	}
	// BuildVMess 输出为所有 vmess:// 链接的 base64
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatalf("vmess 输出非 base64: %v", err)
	}
	link := string(raw)
	if !strings.HasPrefix(link, "vmess://") {
		t.Fatalf("链接应以 vmess:// 开头: %s", link)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		t.Fatalf("vmess:// 内层非 base64: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		t.Fatalf("vmess payload 非 JSON: %v", err)
	}
	if m["add"] != "1.2.3.4" || m["port"].(float64) != 443 || m["id"] != "u1" {
		t.Errorf("vmess 字段错误: %v", m)
	}
}

func TestBuildClash(t *testing.T) {
	items := []ExportItem{{Host: "1.2.3.4", Port: 443, Protocol: "vmess", Transport: "ws", UUID: "u1", Alias: "节点A"}}
	s, err := BuildClash(items)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]interface{}
	if err := yaml.Unmarshal([]byte(s), &cfg); err != nil {
		t.Fatalf("clash 输出非合法 YAML: %v", err)
	}
	proxies, ok := cfg["proxies"].([]interface{})
	if !ok || len(proxies) != 1 {
		t.Fatalf("clash proxies 错误: %v", cfg["proxies"])
	}
	groups, ok := cfg["proxy-groups"].([]interface{})
	if !ok || len(groups) < 1 {
		t.Fatalf("clash proxy-groups 缺失: %v", cfg["proxy-groups"])
	}
}

func TestBuildSIP008(t *testing.T) {
	items := []ExportItem{{Host: "1.2.3.4", Port: 443, Protocol: "vmess", Transport: "tcp", UUID: "u1", Alias: "节点A", Region: "US"}}
	s, err := BuildSIP008(items)
	if err != nil {
		t.Fatal(err)
	}
	var sip map[string]interface{}
	if err := json.Unmarshal([]byte(s), &sip); err != nil {
		t.Fatalf("sip008 输出非合法 JSON: %v", err)
	}
	servers, ok := sip["servers"].([]interface{})
	if !ok || len(servers) != 1 {
		t.Fatalf("sip008 servers 错误: %v", sip["servers"])
	}
}

func TestFlagEmoji(t *testing.T) {
	if FlagEmoji("DE") != "🇩🇪" {
		t.Errorf("DE 国旗错误: %s", FlagEmoji("DE"))
	}
	if FlagEmoji("德国") != "🇩🇪" {
		t.Errorf("德国 国旗错误: %s", FlagEmoji("德国"))
	}
	if FlagEmoji("unknown") != "" {
		t.Errorf("未知区域应返回空: %s", FlagEmoji("unknown"))
	}
}

func TestBuildSurfboardSkipsUnsupported(t *testing.T) {
	items := []ExportItem{
		{Host: "1.2.3.4", Port: 443, Protocol: "vmess", Transport: "tcp", UUID: "u1", Alias: "vm"},
		{Host: "1.2.3.4", Port: 8443, Protocol: "vless", Transport: "tcp", UUID: "u2", Alias: "vl"},  // Surfboard 不支持
		{Host: "1.2.3.4", Port: 9999, Protocol: "vmess", Transport: "grpc", UUID: "u3", Alias: "gr"}, // grpc 不支持
	}
	s, err := BuildSurfboard(items, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s, "vl") {
		t.Error("Surfboard 不应包含 vless 代理")
	}
	if strings.Contains(s, "gr") {
		t.Error("Surfboard 不应包含 grpc 代理")
	}
	if !strings.Contains(s, "vm") {
		t.Error("Surfboard 应包含 vmess 代理")
	}
}
