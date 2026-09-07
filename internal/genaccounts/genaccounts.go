package genaccounts

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/wangdayong228/ydyl-deploy-client/internal/crosstxconfig"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

type Action string

const (
	ActionStart  Action = "start"
	ActionStop   Action = "stop"
	ActionResume Action = "resume"
)

type Params struct {
	ServersPath    string
	Action         Action
	SSHUser        string
	SSHKeyPath     string
	MaxConcurrency int
	Runner         oscmdexec.Runner
}

type sshCallError struct {
	errs []error
}

func (e sshCallError) Error() string {
	if len(e.errs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共有 %d 台机器 gen-accounts 失败：\n", len(e.errs))
	for _, err := range e.errs {
		fmt.Fprintf(&b, "- %s\n", err.Error())
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e sshCallError) Unwrap() []error { return e.errs }

func SelectTargets(servers []deploy.ServerInfo) ([]deploy.ServerInfo, error) {
	entries, err := crosstxconfig.PickChainEntries(servers)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("没有可执行 gen-accounts 的目标节点")
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]deploy.ServerInfo, 0, len(names))
	for _, name := range names {
		out = append(out, entries[name])
	}
	return out, nil
}

func RemoteCommand(action Action) (string, error) {
	switch action {
	case ActionStart, ActionStop, ActionResume:
	default:
		return "", fmt.Errorf("不支持的 action=%q（仅支持 start/stop/resume）", action)
	}
	script := "npm run " + string(action)
	inner := fmt.Sprintf(
		`set -euo pipefail; source "$HOME/.ydyl-env"; cd %s/ydyl-gen-accounts; %s`,
		deploy.RemoteRepoDirDefault,
		script,
	)
	return "bash -lc '" + inner + "'", nil
}

func Run(ctx context.Context, p Params) error {
	if strings.TrimSpace(p.ServersPath) == "" {
		return fmt.Errorf("serversPath 不能为空")
	}
	sshUser := strings.TrimSpace(p.SSHUser)
	if sshUser == "" {
		return fmt.Errorf("sshUser 不能为空")
	}
	keyPath := strings.TrimSpace(p.SSHKeyPath)
	if keyPath == "" {
		return fmt.Errorf("sshKeyPath 不能为空")
	}

	remoteCmd, err := RemoteCommand(p.Action)
	if err != nil {
		return err
	}

	servers, err := crosstxconfig.LoadServers(p.ServersPath)
	if err != nil {
		return err
	}
	targets, err := SelectTargets(servers)
	if err != nil {
		return err
	}

	runner := p.Runner
	if runner == nil {
		if _, err := os.Stat(keyPath); err != nil {
			return fmt.Errorf("SSH 私钥文件不可用: %s: %w", keyPath, err)
		}
		runner = oscmdexec.DefaultRunner
	}

	log.Printf("👉 gen-accounts %s：目标 %d 台，servers=%s\n", p.Action, len(targets), p.ServersPath)
	for _, t := range targets {
		log.Printf("   - %s %s %s\n", t.Name, t.ServiceType, t.IP)
	}

	limit := p.MaxConcurrency
	if limit <= 0 {
		limit = deploy.SSHMaxConcurrency(deploy.CommonConfig{})
	}
	if limit <= 0 {
		limit = 1
	}
	sem := make(chan struct{}, limit)

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, target := range targets {
		t := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				mu.Lock()
				errs = append(errs, fmt.Errorf("[%s][%s] %s 失败: %w", t.IP, t.Name, p.Action, ctx.Err()))
				mu.Unlock()
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()

			spec := oscmdexec.Spec{
				Name: "ssh",
				Args: []string{
					"-o", "StrictHostKeyChecking=no",
					"-o", "IdentitiesOnly=yes",
					"-o", "BatchMode=yes",
					"-i", keyPath,
					fmt.Sprintf("%s@%s", sshUser, t.IP),
					remoteCmd,
				},
			}
			if err := runner(ctx, spec); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("[%s][%s] %s 失败: %w", t.IP, t.Name, p.Action, err))
				mu.Unlock()
				return
			}
			log.Printf("✅ [%s][%s] gen-accounts %s 完成\n", t.IP, t.Name, p.Action)
		}()
	}
	wg.Wait()
	if len(errs) == 0 {
		return nil
	}
	return sshCallError{errs: errs}
}
