package agent

import (
	"log"
	"os/exec"
	"sync"
)

var (
	cmd      *exec.Cmd
	mu       sync.Mutex
	xrayBin  = "/usr/local/bin/xray"
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
	return nil
}

// IsXrayRunning 返回 xray 进程是否在运行
func IsXrayRunning() bool {
	mu.Lock()
	defer mu.Unlock()
	return cmd != nil && cmd.Process != nil
}
