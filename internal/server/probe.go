package server

import (
	"log"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"

	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

const (
	probeInterval       = 60 * time.Second
	dialTimeout         = 3 * time.Second
	failThreshold       = 3 // 连续失败几次触发自愈
	maxSelfHealAttempts = 5 // 最多换端口次数
	offlineThreshold    = 3 * time.Minute
)

// failCounts: inboundID -> 连续失败次数；healAttempts: nodeID -> 已换端口次数（内存态）
var (
	failCounts   = map[uint]int{}
	healAttempts = map[uint]int{}
)

// StartProbeScheduler 启动节点连通性探测与自愈调度器
func StartProbeScheduler() {
	go func() {
		ticker := time.NewTicker(probeInterval)
		probeAll()
		for range ticker.C {
			probeAll()
		}
	}()
}

func probeAll() {
	var nodes []model.Node
	store.DB.Where("enabled = ?", true).Find(&nodes)
	for _, node := range nodes {
		// 整体失联（心跳超时）：直接下线，不改端口
		if time.Since(node.LastHeartbeat) > offlineThreshold {
			store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
				"status":       "offline",
				"connectivity": "offline",
			})
			log.Printf("[probe] node=%d heartbeat timeout, marked offline", node.ID)
			notifyOffline(node)
			continue
		}

		var inbounds []model.Inbound
		store.DB.Where("node_id = ? AND enabled = ?", node.ID, true).Find(&inbounds)
		allOK := true
		host := hostOf(node.Address)
		for _, in := range inbounds {
			addr := net.JoinHostPort(host, strconv.Itoa(in.Port))
			conn, err := net.DialTimeout("tcp", addr, dialTimeout)
			if err != nil {
				failCounts[in.ID]++
				allOK = false
				log.Printf("[probe] node=%d inbound=%d port=%d unreachable (fails=%d)", node.ID, in.ID, in.Port, failCounts[in.ID])
				if failCounts[in.ID] >= failThreshold {
					selfHeal(node, in)
				}
			} else {
				conn.Close()
				failCounts[in.ID] = 0
			}
		}
		if allOK {
			store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Update("connectivity", "ok")
		}
	}
}

// selfHeal 端口不通且 agent 在线时，在端口范围内换端口并重发配置
func selfHeal(node model.Node, in model.Inbound) {
	att := healAttempts[node.ID]
	if att >= maxSelfHealAttempts {
		store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
			"status":       "offline",
			"connectivity": "offline",
		})
		log.Printf("[probe] node=%d self-heal exhausted after %d attempts, marked offline", node.ID, att)
		notifyOffline(node)
		return
	}

	rng := node.PortRange
	if rng == "" {
		rng, _ = store.GetSetting("default_port_range")
	}
	ranges := parseRanges(rng)

	used := map[int]bool{}
	var others []model.Inbound
	store.DB.Where("node_id = ? AND enabled = ?", node.ID, true).Find(&others)
	for _, o := range others {
		used[o.Port] = true
	}
	newPort, ok := pickPort(ranges, used)
	if !ok {
		log.Printf("[probe] node=%d no available port in range %q", node.ID, rng)
		return
	}

	oldPort := in.Port
	store.DB.Model(&model.Inbound{}).Where("id = ?", in.ID).Updates(map[string]interface{}{
		"port":          newPort,
		"port_auto_fixed": true,
	})
	log.Printf("[probe] node=%d inbound=%d self-heal: port %d -> %d", node.ID, in.ID, oldPort, newPort)

	if _, err := syncNode(node); err != nil {
		log.Printf("[probe] node=%d re-dispatch failed: %v", node.ID, err)
	}
	healAttempts[node.ID] = att + 1
	failCounts[in.ID] = 0 // 等待下次探测验证新端口
}

// notifyOffline 节点下线预警钩子（预警模块 P1 未选，先记录日志，预留扩展）
func notifyOffline(node model.Node) {
	log.Printf("[probe] ALERT node=%d (%s) offline", node.ID, node.Name)
}

// ---- 端口范围工具 ----

type portRange struct{ lo, hi int }

func parseRanges(s string) []portRange {
	out := []portRange{}
	s = strings.TrimSpace(s)
	if s == "" {
		return out
	}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			ps := strings.SplitN(part, "-", 2)
			lo, err1 := strconv.Atoi(strings.TrimSpace(ps[0]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(ps[1]))
			if err1 == nil && err2 == nil && lo <= hi {
				out = append(out, portRange{lo, hi})
			}
		} else if p, err := strconv.Atoi(part); err == nil {
			out = append(out, portRange{p, p})
		}
	}
	return out
}

func pickPort(ranges []portRange, used map[int]bool) (int, bool) {
	candidates := []int{}
	for _, r := range ranges {
		for p := r.lo; p <= r.hi; p++ {
			if !used[p] {
				candidates = append(candidates, p)
			}
		}
	}
	if len(candidates) == 0 {
		return 0, false
	}
	return candidates[rand.Intn(len(candidates))], true
}
