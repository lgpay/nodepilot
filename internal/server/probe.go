package server

import (
	"fmt"
	"log"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"nodepilot/internal/model"
	"nodepilot/internal/notify"
	"nodepilot/internal/store"
)

const (
	probeInterval       = 60 * time.Second
	dialTimeout         = 3 * time.Second
	failThreshold       = 3 // 连续失败几次触发自愈
	maxSelfHealAttempts = 5 // 最多换端口次数
	offlineThreshold    = 3 * time.Minute
)

// probeState 探测调度器的内存态（连续失败次数 / 自愈次数 / 上次自愈时间 / 是否曾离线）。
// 原先为裸全局 map，存在并发隐患；现统一由互斥锁保护，避免 data race。
type probeState struct {
	mu           sync.Mutex
	failCounts   map[uint]int
	healAttempts map[uint]int
	lastHeal     map[uint]time.Time
	wasOffline   map[uint]bool
}

var ps = &probeState{
	failCounts:   map[uint]int{},
	healAttempts: map[uint]int{},
	lastHeal:     map[uint]time.Time{},
	wasOffline:   map[uint]bool{},
}

func (s *probeState) incFail(id uint) {
	s.mu.Lock()
	s.failCounts[id]++
	s.mu.Unlock()
}

func (s *probeState) resetFail(id uint) {
	s.mu.Lock()
	s.failCounts[id] = 0
	s.mu.Unlock()
}

func (s *probeState) getFail(id uint) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.failCounts[id]
}

func (s *probeState) getHeal(id uint) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.healAttempts[id]
}

func (s *probeState) setHeal(id uint, v int) {
	s.mu.Lock()
	s.healAttempts[id] = v
	s.mu.Unlock()
}

func (s *probeState) getLastHeal(id uint) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.lastHeal[id]
	return t, ok
}

func (s *probeState) setLastHeal(id uint, t time.Time) {
	s.mu.Lock()
	s.lastHeal[id] = t
	s.mu.Unlock()
}

func (s *probeState) setOffline(id uint, v bool) {
	s.mu.Lock()
	s.wasOffline[id] = v
	s.mu.Unlock()
}

func (s *probeState) wasOfflineNow(id uint) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wasOffline[id]
}

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
			ps.setOffline(node.ID, true)
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
				ps.incFail(in.ID)
				allOK = false
				log.Printf("[probe] node=%d inbound=%d port=%d unreachable (fails=%d)", node.ID, in.ID, in.Port, ps.getFail(in.ID))
				// 仅当自动修复间隔>0（分钟）且连续失败达阈值、且距上次自愈已过最小间隔时才换端口
				if in.AutoHealInterval > 0 && ps.getFail(in.ID) >= failThreshold {
					if last, ok := ps.getLastHeal(node.ID); !ok || time.Since(last) >= time.Duration(in.AutoHealInterval)*time.Minute {
						selfHeal(node, in)
					}
				}
			} else {
				conn.Close()
				ps.resetFail(in.ID)
			}
		}
		if allOK {
			prev := node.Connectivity
			store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Update("connectivity", "ok")
			// 状态由 offline/degraded 切回 ok：触发「恢复在线」通知（只发一次）
			if ps.wasOfflineNow(node.ID) && prev != "ok" {
				ps.setOffline(node.ID, false)
				notify.Dispatch("node_recovered", "✅ 节点恢复在线", fmt.Sprintf("节点 #%d (%s) 代理端口已恢复可达", node.ID, node.Name))
			}
		}
	}
}

// selfHeal 端口不通且 agent 在线时，在端口范围内换端口并重发配置
func selfHeal(node model.Node, in model.Inbound) {
	att := ps.getHeal(node.ID)
	if att >= maxSelfHealAttempts {
		store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
			"status":       "offline",
			"connectivity": "offline",
		})
		log.Printf("[probe] node=%d self-heal exhausted after %d attempts, marked offline", node.ID, att)
		ps.setOffline(node.ID, true)
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
		"port":           newPort,
		"port_auto_fixed": true,
	})
	log.Printf("[probe] node=%d inbound=%d self-heal: port %d -> %d", node.ID, in.ID, oldPort, newPort)

	notifyHealed(node, in, oldPort, newPort)

	if _, err := syncNode(node); err != nil {
		log.Printf("[probe] node=%d re-dispatch failed: %v", node.ID, err)
	}
	ps.setHeal(node.ID, att+1)
	ps.setLastHeal(node.ID, time.Now())
	ps.resetFail(in.ID) // 等待下次探测验证新端口
}

// notifyOffline 节点下线预警（心跳超时或自愈耗尽）
func notifyOffline(node model.Node) {
	notify.Dispatch("node_offline", "🔴 节点离线", fmt.Sprintf("节点 #%d (%s) 已离线（心跳超时或自愈尝试耗尽）", node.ID, node.Name))
}

// notifyHealed 节点自愈成功（换端口后恢复）
func notifyHealed(node model.Node, in model.Inbound, oldPort, newPort int) {
	notify.Dispatch("node_healed", "🟡 节点已自愈", fmt.Sprintf("节点 #%d (%s) 入站 #%d 端口 %d → %d 已切换并恢复", node.ID, node.Name, in.ID, oldPort, newPort))
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
			seg := strings.SplitN(part, "-", 2)
			lo, err1 := strconv.Atoi(strings.TrimSpace(seg[0]))
			hi, err2 := strconv.Atoi(strings.TrimSpace(seg[1]))
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
