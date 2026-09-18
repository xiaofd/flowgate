// Package svc 封装跨平台系统服务（Windows 服务 / systemd）的安装与运行。
package svc

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/kardianos/service"

	"flowgate/internal/cfg"
	"flowgate/internal/sbx"
	"flowgate/internal/setup"
)

// Name 系统服务名。
const Name = "FlowGate"

// Program kardianos 服务程序：启动时自动初始化（含官方客户端安装）并守护核心。
// 初始化在后台 goroutine 中进行并失败重试，避免 Windows SCM 30 秒启动超时
// （首次自动安装官方客户端可能耗时数分钟）。
type Program struct {
	home     string
	core     *sbx.Core
	stopCh   chan struct{}
	stopOnce sync.Once
	done     chan struct{}
}

// Start 服务启动：立即返回，后台完成初始化并拉起核心守护。
func (p *Program) Start(s service.Service) error {
	paths := cfg.Resolve(p.home)
	lg, err := openLogger(paths)
	if err != nil {
		return err
	}
	p.stopCh = make(chan struct{})
	p.done = make(chan struct{})
	go p.run(paths, lg)
	return nil
}

func (p *Program) run(paths cfg.Paths, lg *log.Logger) {
	defer close(p.done)
	// 初始化（幂等），失败则退避重试直至成功或收到停止信号
	for {
		if err := setup.Ensure(paths, lg.Printf); err != nil {
			lg.Printf("[service] 初始化失败: %v，30 秒后重试", err)
			select {
			case <-time.After(30 * time.Second):
				continue
			case <-p.stopCh:
				return
			}
		}
		break
	}
	select {
	case <-p.stopCh:
		return
	default:
	}
	p.core = sbx.NewCore(paths, lg, false)
	if err := p.core.Run(); err != nil {
		lg.Printf("[service] 核心守护异常退出: %v", err)
	}
}

// Stop 服务停止：优雅停止核心并等待后台任务退出。
func (p *Program) Stop(s service.Service) error {
	if p.stopCh != nil {
		p.stopOnce.Do(func() { close(p.stopCh) })
	}
	if p.core != nil {
		p.core.Shutdown()
	}
	if p.done != nil {
		select {
		case <-p.done:
		case <-time.After(35 * time.Second):
		}
	}
	return nil
}

// New 构建服务管理器。
func New(home string) (service.Service, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	sc := &service.Config{
		Name:        Name,
		DisplayName: "FlowGate",
		Description: "FlowGate 智能分流：仅 GFW 名单流量经 Cloudflare WARP 转发，其余直连",
		Executable:  exe,
		Arguments:   svcArgs(home),
	}
	return service.New(&Program{home: home}, sc)
}

func svcArgs(home string) []string {
	var args []string
	if home != "" {
		args = append(args, "--home", home)
	}
	return append(args, "--svc")
}

func openLogger(p cfg.Paths) (*log.Logger, error) {
	if err := os.MkdirAll(p.LogDir, 0o755); err != nil {
		return nil, err
	}
	// 服务日志超过 5MB 时轮转，避免长期运行无限增长
	if st, err := os.Stat(p.SvcLog); err == nil && st.Size() > 5<<20 {
		_ = os.Rename(p.SvcLog, p.SvcLog+".old")
	}
	f, err := os.OpenFile(p.SvcLog,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return log.New(f, "", log.LstdFlags), nil
}
