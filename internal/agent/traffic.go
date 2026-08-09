package agent

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// xrayStatsAddr 必须与控制面生成配置里的 api 监听地址一致（configgen.xrayStatsAddr）。
const xrayStatsAddr = "127.0.0.1:10085"

// trafficStatItem 单次采集的按用户流量增量（已用 -reset 取得距上次采集的差值）。
type trafficStatItem struct {
	Email string `json:"email"`
	Up    int64  `json:"up"`
	Down  int64  `json:"down"`
}

// StartTrafficCollector 周期向本机 xray stats API 查询按用户累计流量并上报控制面。
// 写法镜像 StartHeartbeat（http.go）。
func StartTrafficCollector(interval time.Duration) {
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for range t.C {
			collectTraffic()
		}
	}()
}

// collectTraffic 执行 `xray api statsquery` 解析按用户流量，POST 到控制面 /nodes/:id/traffic。
func collectTraffic() {
	out, err := exec.Command(xrayBin, "api", "statsquery", "--server="+xrayStatsAddr, "--pattern=user>>>", "--reset").Output()
	if err != nil {
		// xray 未运行 / stats 未启用时静默跳过，不刷屏
		return
	}
	var resp struct {
		Stat []struct {
			Name  string          `json:"name"`
			Value json.RawMessage `json:"value"` // 兼容 number 与 string 两种形态
		} `json:"stat"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		log.Printf("[agent] traffic parse error: %v", err)
		return
	}

	byEmail := map[string]*trafficStatItem{}
	for _, s := range resp.Stat {
		v, ok := parseStatValue(s.Value)
		if !ok {
			continue
		}
		// 计数键形如 user>>>email>>>traffic>>>uplink|downlink
		parts := strings.Split(s.Name, ">>>")
		if len(parts) != 4 || parts[0] != "user" || parts[2] != "traffic" {
			continue
		}
		email := parts[1]
		it, ok := byEmail[email]
		if !ok {
			it = &trafficStatItem{Email: email}
			byEmail[email] = it
		}
		switch parts[3] {
		case "uplink":
			it.Up += v
		case "downlink":
			it.Down += v
		}
	}
	if len(byEmail) == 0 {
		return
	}
	items := make([]trafficStatItem, 0, len(byEmail))
	for _, it := range byEmail {
		items = append(items, *it)
	}

	body, _ := json.Marshal(map[string]interface{}{"stats": items})
	url := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/nodes/" + cfg.NodeID + "/traffic"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] traffic build error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	client := &http.Client{Timeout: 10 * time.Second}
	resp2, err := client.Do(req)
	if err != nil {
		log.Printf("[agent] traffic upload failed: %v", err)
		return
	}
	resp2.Body.Close()
}

// parseStatValue 解析 xray stats 的 value：兼容 JSON number 与字符串两种形态。
func parseStatValue(raw json.RawMessage) (int64, bool) {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" {
		return 0, false
	}
	if strings.HasPrefix(t, "\"") {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return 0, false
		}
		n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if err != nil {
			return 0, false
		}
		return n, true
	}
	n, err := strconv.ParseInt(t, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
