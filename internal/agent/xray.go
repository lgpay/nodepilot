package agent

import (
	"fmt"
	"log"
	"os/exec"
	"sync"
)

var (
	cmd        *exec.Cmd
	mu         sync.Mutex
	xrayBin    = "/usr/local/bin/xray"
	configPath string
)

// SetXrayBin 设置 xray 二进制路径
func SetXrayBin(p string) {
	if p != "" {
		xrayBin = p
	}
}

// SetConfigPath 设置配置文件路径（用于状态展示）
func SetConfigPath(p string) {
	configPath = p
}

// Validate 用 `xray run -test` 校验配置是否合法（不启动进程）。
// xray 二进制缺失时跳过校验（与 Restart 的优雅降级一致）。
func Validate(path string) error {
	if _, err := exec.LookPath(xrayBin); err != nil {
		return nil
	}
	cmd := exec.Command(xrayBin, "run", "-test", "-config", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("xray 配置校验失败: %v\n%s", err, string(out))
	}
	return nil
}

// Restart 重启 xray：kill 旧进程后启动新进程（MVP 用重启实现 reload）
func Restart(path string) error {
	mu.Lock()
	defer mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}
	if _, err := exec.LookPath(xrayBin); err != nil {
		log.Printf("[agent] xray binary not found at %s, skip start (config written to %s)", xrayBin, path)
		// 优雅降级：配置已落盘，MVP 演示下发生成；真实部署需预置 xray
		return nil
	}
	c := exec.Command(xrayBin, "run", "-c", path)
	c.Stdout = log.Writer()
	c.Stderr = log.Writer()
	if err := c.Start(); err != nil {
		return err
	}
	cmd = c
	configPath = path
	log.Printf("[agent] xray started (pid=%d)", c.Process.Pid)
	// 进程退出时清空 cmd，保证 IsXrayRunning 准确（崩溃后看护才能拉起）
	go func(pc *exec.Cmd) {
		_ = pc.Wait()
		mu.Lock()
		if cmd == pc { // 仅当仍指向当前 xray 进程时清空，避免误清重启后的新进程
			cmd = nil
			log.Printf("[agent] xray process exited (pid=%d)", pc.Process.Pid)
		}
		mu.Unlock()
	}(c)
	return nil
}

// IsXrayRunning 返回 xray 进程是否在运行
func IsXrayRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return cmd != nil && cmd.Process != nil
}

// EnsureXrayRunning 看护：已有落盘配置但 xray 进程不在运行时自动拉起（崩溃自愈）。
// 由心跳循环周期调用。
func EnsureXrayRunning() {
	mu.Lock()
	p := configPath
	mu.Unlock()
	if p == "" || IsXrayRunning() {
		return
	}
	log.Printf("[agent] xray not running, auto-restart with %s", p)
	if err := Restart(p); err != nil {
		log.Printf("[agent] xray auto-restart failed: %v", err)
	}
}
