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
	hbFailThreshold     = 3 // 连续几次心跳未到达即判节点离线（不再用时间阈值）
)

// probeState 探测调度器的内存态（连续失败次数 / 自愈次数 / 上次自愈时间 / 是否曾离线）。
// 原先为裸全局 map，存在并发隐患；现统一由互斥锁保护，避免 data race。
type probeState struct {
	mu           sync.Mutex
	failCounts   map[uint]int
	healAttempts map[uint]int
	lastHeal     map[uint]time.Time
	wasOffline   map[uint]bool
	connState    map[uint]string // 入站ID → ok|fail（最近一次端口连通探测结果）
	lastProbe    map[uint]time.Time // 入站ID → 最近一次探测时间（探测按自动修复间隔调度）
	hbFailCounts map[uint]int      // 节点ID → 连续未收到心跳次数
	hbLastSeen   map[uint]time.Time // 节点ID → 已计账(已确认收到)的最近心跳时间
}

var ps = &probeState{
	failCounts:   map[uint]int{},
	healAttempts: map[uint]int{},
	lastHeal:     map[uint]time.Time{},
	wasOffline:   map[uint]bool{},
	connState:    map[uint]string{},
	lastProbe:    map[uint]time.Time{},
	hbFailCounts: map[uint]int{},
	hbLastSeen:   map[uint]time.Time{},
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

func (s *probeState) setConn(id uint, state string) {
	s.mu.Lock()
	s.connState[id] = state
	s.mu.Unlock()
}

func (s *probeState) getConn(id uint) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.connState[id]; ok {
		return v
	}
	return ""
}

func (s *probeState) getLastProbe(id uint) (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.lastProbe[id]
	return t, ok
}

func (s *probeState) setLastProbe(id uint, t time.Time) {
	s.mu.Lock()
	s.lastProbe[id] = t
	s.mu.Unlock()
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

func (s *probeState) incHbFail(id uint) {
	s.mu.Lock()
	s.hbFailCounts[id]++
	s.mu.Unlock()
}

func (s *probeState) resetHbFail(id uint) {
	s.mu.Lock()
	s.hbFailCounts[id] = 0
	s.mu.Unlock()
}

func (s *probeState) getHbFail(id uint) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hbFailCounts[id]
}

func (s *probeState) getHbLastSeen(id uint) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hbLastSeen[id]
}

func (s *probeState) setHbLastSeen(id uint, t time.Time) {
	s.mu.Lock()
	s.hbLastSeen[id] = t
	s.mu.Unlock()
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
	// 各节点并行探测（节点间无共享可变状态，ps 状态表已由互斥锁保护）
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(n model.Node) {
			defer wg.Done()
			probeNode(n)
		}(node)
	}
	wg.Wait()
}

// isNodeExpired 判断节点是否已到期（纯函数，便于单测）
func isNodeExpired(n model.Node) bool {
	return n.ExpiresAt != nil && !n.ExpiresAt.IsZero() && time.Now().After(*n.ExpiresAt)
}

// probeNode 对单个节点执行一轮探测：到期停用 → 心跳超时下线 → 端口连通性探测/自愈。
func probeNode(node model.Node) {
	// 到期节点：直接停用（enabled=false），不再下发配置与探测
	if isNodeExpired(node) {
		store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
			"enabled":      false,
			"status":       "offline",
			"connectivity": "offline",
		})
		log.Printf("[probe] node=%d expired at %s, disabled", node.ID, node.ExpiresAt.Format(time.RFC3339))
		key := fmt.Sprintf("%d:expired:%s", node.ID, node.ExpiresAt.Format("2006-01-02"))
		if markAlerted(key) {
			notify.Dispatch("node_expired", "🔴 节点已到期", fmt.Sprintf("节点「%s」已于 %s 到期，已自动停用",
				node.Name, node.ExpiresAt.Format("2006-01-02 15:04")))
		}
		return
	}
	// 整体失联（心跳连续未到达）：直接下线，不改端口。
	// 不再用时间阈值：期望每 heartbeat_interval 收到一次心跳，连续 hbFailThreshold(3) 次
	// 心跳未到达即判离线。hbLastSeen 记录「已确认收到/已计账」的最近心跳时间，用于逐拍
	// 计数，避免每个探测周期重复 +1。
	seen := ps.getHbLastSeen(node.ID)
	if seen.IsZero() {
		seen = node.LastHeartbeat
		ps.setHbLastSeen(node.ID, seen)
	}
	if node.LastHeartbeat.After(seen) {
		// 收到了新心跳：重置失败计数，并把基准推进到最新心跳时间
		ps.setHbLastSeen(node.ID, node.LastHeartbeat)
		ps.resetHbFail(node.ID)
	} else if node.HeartbeatInterval > 0 {
		// 没有新心跳：若距上次已计账心跳已超过一个周期，记一次「心跳未到达」，并推进基准
		if time.Since(seen) > time.Duration(node.HeartbeatInterval)*time.Second {
			ps.setHbLastSeen(node.ID, seen.Add(time.Duration(node.HeartbeatInterval)*time.Second))
			ps.incHbFail(node.ID)
		}
	} else {
		// 未知心跳间隔（极少数情况）：回退固定 3 分钟时间阈值，避免误判
		if time.Since(node.LastHeartbeat) > offlineThreshold {
			ps.incHbFail(node.ID)
		} else {
			ps.resetHbFail(node.ID)
		}
	}
	if ps.getHbFail(node.ID) >= hbFailThreshold {
		store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Updates(map[string]interface{}{
			"status":       "offline",
			"connectivity": "offline",
		})
		log.Printf("[probe] node=%d heartbeat missed %d times, marked offline", node.ID, ps.getHbFail(node.ID))
		// 仅当节点状态由在线切换为离线时发一次离线通知；持续离线期间不再重复推送。
		// 判据结合内存态与 DB 状态：控制面重启后内存态丢失，但 DB 中 status 仍为 offline，
		// 此时不重复通知；恢复在线后再离线会重新触发（与 node_recovered 的通知机制对称）。
		alreadyOffline := ps.wasOfflineNow(node.ID) || node.Status == "offline"
		ps.setOffline(node.ID, true)
		if !alreadyOffline {
			notifyOffline(node)
		}
		return
	}

	var inbounds []model.Inbound
	store.DB.Where("node_id = ? AND enabled = ?", node.ID, true).Find(&inbounds)
	allOK := true
	host := hostOf(node.Address)
	now := time.Now()
	for _, in := range inbounds {
		// 探测间隔：
		// - AutoHealInterval=0（永不换端口）：正常 60 分钟低频检测连通；
		//   失败后同样进入 60s 快速重试，保证上报准确的连通状态（但不换端口）
		// - 正常状态(fail=0)：跟随自动修复间隔(AutoHealInterval 分钟)
		// - 失败后(fail>0)：60s 快速重试，尽早确认故障并换端口恢复
		var interval time.Duration
		if in.AutoHealInterval == 0 {
			if ps.getFail(in.ID) == 0 {
				interval = 60 * time.Minute
			} else {
				interval = probeInterval
			}
		} else if ps.getFail(in.ID) == 0 {
			interval = time.Duration(in.AutoHealInterval) * time.Minute
		} else {
			interval = probeInterval
		}
		if last, ok := ps.getLastProbe(in.ID); ok && now.Sub(last) < interval {
			// 未到探测时间：用最近连通状态参与节点整体判断，本次不重复探测
			if ps.getConn(in.ID) != "ok" {
				allOK = false
			}
			continue
		}
		ps.setLastProbe(in.ID, now)

		addr := net.JoinHostPort(host, strconv.Itoa(in.Port))
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			ps.setConn(in.ID, "fail")
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
			ps.setConn(in.ID, "ok")
			ps.resetFail(in.ID)
		}
	}
	if allOK {
		prev := node.Connectivity
		store.DB.Model(&model.Node{}).Where("id = ?", node.ID).Update("connectivity", "ok")
		// 端口全部恢复可达：重置自愈次数与入站失败计数，保证下次故障时自愈流程从零开始完整执行
		ps.setHeal(node.ID, 0)
		for _, in := range inbounds {
			ps.resetFail(in.ID)
		}
		// 状态由 offline/degraded 切回 ok：触发「恢复在线」通知（只发一次）
		if ps.wasOfflineNow(node.ID) && prev != "ok" {
			ps.setOffline(node.ID, false)
			notify.Dispatch("node_recovered", "✅ 节点恢复在线", fmt.Sprintf("节点「%s」代理端口已恢复可达", node.Name))
			// 离线期间可能改过配置：恢复时自动把当前配置重推一次（与心跳恢复路径互补，
			// 此处覆盖「端口/自愈恢复」而心跳路径覆盖「心跳恢复」，二者互斥不重复推送）。
			go func(n model.Node) {
				if _, err := syncNode(n); err != nil {
					log.Printf("[probe] auto-sync on recovery node=%d failed: %v", n.ID, err)
				}
			}(node)
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
		// 与心跳超时分支一致：仅在首次切换为离线时通知一次，避免持续离线刷屏
		alreadyOffline := ps.wasOfflineNow(node.ID) || node.Status == "offline"
		ps.setOffline(node.ID, true)
		if !alreadyOffline {
			notifyHealFailed(node, att)
		}
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
		"port":            newPort,
		"port_auto_fixed": true,
		"heal_count":      in.HealCount + 1,
	})
	ps.setConn(in.ID, "fail")       // 新端口待下一轮探测确认
	ps.setLastProbe(in.ID, time.Time{}) // 重置探测调度，下一轮立即探测新端口
	log.Printf("[probe] node=%d inbound=%d self-heal: port %d -> %d", node.ID, in.ID, oldPort, newPort)

	notifyHealed(node, in, oldPort, newPort)

	if _, err := syncNode(node); err != nil {
		log.Printf("[probe] node=%d re-dispatch failed: %v", node.ID, err)
	}
	ps.setHeal(node.ID, att+1)
	ps.setLastHeal(node.ID, time.Now())
	ps.resetFail(in.ID) // 等待下次探测验证新端口
}

// notifyOffline 节点因心跳超时离线预警（与修复失败分支区分）
func notifyOffline(node model.Node) {
	notify.Dispatch("node_offline", "🔴 节点离线", fmt.Sprintf("节点「%s」心跳超时，已离线", node.Name))
}

// notifyHealFailed 节点因端口修复尝试耗尽离线预警（与心跳超时分支区分）
func notifyHealFailed(node model.Node, attempts int) {
	notify.Dispatch("node_heal_failed", "🔴 节点离线（修复失败）", fmt.Sprintf("节点「%s」代理端口修复失败，已离线", node.Name))
}

// notifyHealed 节点修复成功（换端口后恢复）
// 天级去重：同一节点当天只通知一次，避免端口反复故障时修复连发刷屏。
func notifyHealed(node model.Node, in model.Inbound, oldPort, newPort int) {
	key := fmt.Sprintf("%d:healed:%s", node.ID, time.Now().UTC().Format("2006-01-02"))
	if !markAlerted(key) {
		return
	}
	notify.Dispatch("node_healed", "🟡 节点已修复", fmt.Sprintf("节点「%s」入站端口 %d → %d 已修复并恢复", node.Name, oldPort, newPort))
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

// pickPort 在端口范围内随机采样一个未被占用的端口。
// 先按总端口数做均匀随机映射，冲突时重试；极端情况（池小/高度占用）回退顺序扫描。
func pickPort(ranges []portRange, used map[int]bool) (int, bool) {
	total := 0
	for _, r := range ranges {
		total += r.hi - r.lo + 1
	}
	if total == 0 {
		return 0, false
	}
	// 把随机序号映射到具体端口
	portAt := func(n int) (int, bool) {
		for _, r := range ranges {
			size := r.hi - r.lo + 1
			if n < size {
				return r.lo + n, true
			}
			n -= size
		}
		return 0, false
	}
	for i := 0; i < 64; i++ {
		if p, ok := portAt(rand.Intn(total)); ok && !used[p] {
			return p, true
		}
	}
	// 重试未命中（池小且近满）：顺序扫描兜底
	for _, r := range ranges {
		for p := r.lo; p <= r.hi; p++ {
			if !used[p] {
				return p, true
			}
		}
	}
	return 0, false
}
