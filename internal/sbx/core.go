package sbx

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"flowgate/internal/cfg"
)

// runCore 执行核心子命令并返回合并输出（带超时）。
func runCore(p cfg.Paths, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.CorePath, args...)
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// RunCoreVersion 返回核心版本描述（首行）。
func RunCoreVersion(p cfg.Paths) (string, error) {
	out, err := runCore(p, "version")
	if err != nil {
		return "", err
	}
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return "", nil
	}
	return strings.TrimSpace(lines[0]), nil
}

// Core 守护 fg-core 子进程：启动、崩溃退避重启、优雅停止。
type Core struct {
	p          cfg.Paths
	log        *log.Logger
	interactive bool

	mu   sync.Mutex
	cmd  *exec.Cmd
	logF *os.File
	proc *procHandle

	stopCh   chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// NewCore 创建守护器。interactive 为 true 时核心日志同时输出到当前控制台。
func NewCore(p cfg.Paths, logger *log.Logger, interactive bool) *Core {
	if logger == nil {
		logger = log.New(io.Discard, "", log.LstdFlags)
	}
	return &Core{
		p:           p,
		log:         logger,
		interactive: interactive,
		proc:        newProc(),
		stopCh:      make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Run 阻塞运行，直至 Shutdown 被调用。核心异常退出时按退避策略自动重启。
func (c *Core) Run() error {
	defer close(c.done)
	defer c.proc.onClose()
	backoff := time.Second
	for {
		err := c.start()
		if err != nil {
			c.log.Printf("[supervisor] 核心启动失败: %v", err)
		} else {
			started := time.Now()
			werr := c.wait()
			c.afterExit()
			if c.isStopping() {
				return nil
			}
			c.log.Printf("[supervisor] 核心异常退出: %v（本次运行 %v），%v 后自动重启",
				werr, time.Since(started).Round(time.Second), backoff)
			// 稳定运行超过 1 分钟则重置退避
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
		}
		select {
		case <-c.stopCh:
			return nil
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

// Shutdown 优雅停止核心并等待 Run 返回。
func (c *Core) Shutdown() {
	c.stopOnce.Do(func() { close(c.stopCh) })
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		c.proc.onStop(cmd)
	}
	select {
	case <-c.done:
	case <-time.After(30 * time.Second):
	}
}

func (c *Core) isStopping() bool {
	select {
	case <-c.stopCh:
		return true
	default:
		return false
	}
}

func (c *Core) start() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 单实例互斥：已有核心在运行（如另一 flowgate 实例）则拒绝启动，
	// 避免两个实例争抢 TUN 接口导致反复崩溃重启
	if pid, running := RunningPid(c.p.PidFile); running {
		return fmt.Errorf("已有核心实例运行中 (PID %d)，请先停止", pid)
	}
	if err := c.prepareLog(); err != nil {
		return err
	}
	cmd := exec.Command(c.p.CorePath, "run", "-c", c.p.SBConfig)
	cmd.Dir = c.p.DataDir
	c.proc.beforeStart(cmd)
	if c.interactive {
		cmd.Stdout = io.MultiWriter(os.Stdout, c.logF)
		cmd.Stderr = io.MultiWriter(os.Stderr, c.logF)
	} else {
		cmd.Stdout = c.logF
		cmd.Stderr = c.logF
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	c.proc.afterStart(cmd)
	c.cmd = cmd
	_ = os.WriteFile(c.p.PidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
	c.log.Printf("[supervisor] 核心已启动 (PID %d)", cmd.Process.Pid)
	return nil
}

func (c *Core) wait() error {
	c.mu.Lock()
	cmd := c.cmd
	c.mu.Unlock()
	if cmd == nil {
		return fmt.Errorf("核心未运行")
	}
	return cmd.Wait()
}

func (c *Core) afterExit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = os.Remove(c.p.PidFile)
	c.cmd = nil
}

// prepareLog 打开核心日志文件，超过 10MB 时轮转。
func (c *Core) prepareLog() error {
	_ = os.MkdirAll(c.p.LogDir, 0o755)
	if st, err := os.Stat(c.p.CoreLog); err == nil && st.Size() > 10<<20 {
		_ = os.Rename(c.p.CoreLog, c.p.CoreLog+".old")
	}
	f, err := os.OpenFile(c.p.CoreLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if c.logF != nil {
		_ = c.logF.Close()
	}
	c.logF = f
	return nil
}

// RunningPid 读取 PID 文件并判断核心进程是否存活。
func RunningPid(pidFile string) (int, bool) {
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, pidAlive(pid)
}
