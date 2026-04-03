package deploy

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/nft-rainbow/rainbow-goutils/utils/commonutils"
	"github.com/openweb3/go-sdk-common/privatekeyhelper"
	"github.com/openweb3/web3go"
	"github.com/openweb3/web3go/interfaces"
	"github.com/openweb3/web3go/signers"
	"github.com/openweb3/web3go/types"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/cryptoutil"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/sshutil"
)

// Deployer 承载一次部署执行所需的上下文与依赖，避免参数层层传递。
// 设计目标：保持对外 Run(ctx,cfg) 兼容，内部以方法组织部署动作。
type Deployer struct {
	ctx context.Context
	cfg DeployConfig

	ec2Client  *ec2.EC2
	outputMgr  *OutputManager
	sshKeyPath string
	// 同一轮 deploy 固定随机段，用于 L1 vault 私钥派生路径倒数第三段。
	l1VaultDeriveRand uint32

	// reuseFromSnapshot 为 true 时从 importedSnapshot 取 IP，跳过 EC2 创建。
	reuseFromSnapshot bool
	importedSnapshot  []CreatedServerInfo
}

const (
	defaultSSHMaxConcurrency  = 100
	defaultSSHReadyRetryCount = 3
	defaultSSHReadyRetryWait  = 5 * time.Second
	deployRestoreMaxRetries   = 3
	defaultSSHAcquireMaxRound = 1
)

var waitSSHReadyFunc = sshutil.WaitSSH

// NewDeployer 负责初始化一次部署执行所需的基础依赖（目录/输出管理/AWS client/SSH key 路径）。
func NewDeployer(ctx context.Context, cfg DeployConfig, opts RunOptions) (*Deployer, error) {
	if err := cfg.CheckValid(); err != nil {
		return nil, err
	}

	// outputDir 为空时沿用既有默认规则（位于 logDir 下的 output）。
	if cfg.CommonConfig.OutputDir == "" {
		cfg.CommonConfig.OutputDir = filepath.Join(cfg.CommonConfig.LogDir, "output")
	}

	// 0) 预先归档旧 output / logs，且两者共享同一时间戳
	archiveTS, err := resolveDeployArchiveTimestamp(cfg.CommonConfig.OutputDir, cfg.CommonConfig.LogDir)
	if err != nil {
		return nil, fmt.Errorf("计算归档时间戳失败: %w", err)
	}
	if _, err := rotateExistingDirWithTimestamp(cfg.CommonConfig.OutputDir, archiveTS); err != nil {
		return nil, fmt.Errorf("归档旧的输出目录失败: %w", err)
	}
	if _, err := rotateExistingDirWithTimestamp(cfg.CommonConfig.LogDir, archiveTS); err != nil {
		return nil, fmt.Errorf("归档旧的日志目录失败: %w", err)
	}

	// 1) 准备日志目录
	if err := os.MkdirAll(cfg.CommonConfig.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 2) 创建 output 目录，用于保存 servers.json / script_status.json
	if err := os.MkdirAll(cfg.CommonConfig.OutputDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建输出目录失败: %w", err)
	}

	outputMgr := NewOutputManager(cfg.CommonConfig.OutputDir)

	// 4) 初始化 AWS session / EC2 client
	awsCfg := aws.Config{}
	if cfg.CommonConfig.Region != "" {
		awsCfg.Region = aws.String(cfg.CommonConfig.Region)
	}

	sess, err := session.NewSession(&awsCfg)
	if err != nil {
		return nil, fmt.Errorf("创建 AWS Session 失败: %w", err)
	}
	ec2Client := ec2.New(sess)

	// 5) 预计算 SSH key 路径
	keyPath := buildSSHKeyPath(cfg.CommonConfig)
	deriveRand, err := generateL1VaultDeriveRand()
	if err != nil {
		return nil, fmt.Errorf("生成 L1 vault 派生随机段失败: %w", err)
	}

	reuseFromSnapshot := false
	var importedSnapshot []CreatedServerInfo
	if strings.TrimSpace(opts.ServersCreateJSONPath) != "" {
		importedSnapshot, err = LoadCreatedServersFromFile(opts.ServersCreateJSONPath)
		if err != nil {
			return nil, fmt.Errorf("加载 servers_create 快照失败（%s）: %w", opts.ServersCreateJSONPath, err)
		}
		reuseFromSnapshot = true
		log.Printf("✅ 已加载 servers_create 快照: path=%s, 有效条目=%d\n", opts.ServersCreateJSONPath, len(importedSnapshot))
	}

	return &Deployer{
		ctx:               ctx,
		cfg:               cfg,
		ec2Client:         ec2Client,
		outputMgr:         outputMgr,
		sshKeyPath:        keyPath,
		l1VaultDeriveRand: deriveRand,
		reuseFromSnapshot: reuseFromSnapshot,
		importedSnapshot:  importedSnapshot,
	}, nil
}

func generateL1VaultDeriveRand() (uint32, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(b[:]), nil
}

// Run 按照 DeployConfig 中的参数，完成一次完整的批量部署流程：
// 对每个 ServiceConfig：
// 1）批量创建对应数量的 EC2 实例；2）等待实例 running；3）获取公网 IP 并等待 SSH 就绪；
// 4）为每个实例构造远程命令并执行；5）收集日志与执行结果。
func Run(ctx context.Context, cfg DeployConfig) error {
	return RunWithOptions(ctx, cfg, RunOptions{})
}

// RunWithOptions 与 Run 相同，可通过 opts 指定复用 servers_create 快照等行为。
func RunWithOptions(ctx context.Context, cfg DeployConfig, opts RunOptions) error {
	d, err := NewDeployer(ctx, cfg, opts)
	if err != nil {
		return err
	}
	return d.Run()
}

// RunWithRestoreRetry 在 deploy 完成后，如果仍存在 failed 节点，则按 deploy-restore 流程最多重试 3 次。
func RunWithRestoreRetry(ctx context.Context, cfg DeployConfig) error {
	return RunWithRestoreRetryWithOptions(ctx, cfg, RunOptions{})
}

// RunWithRestoreRetryWithOptions 与 RunWithRestoreRetry 相同，支持传入 RunOptions。
func RunWithRestoreRetryWithOptions(ctx context.Context, cfg DeployConfig, opts RunOptions) error {
	deployErr := RunWithOptions(ctx, cfg, opts)

	failedIPs, listErr := listFailedIPsFromOutput(cfg.CommonConfig)
	if listErr != nil {
		if deployErr != nil {
			return fmt.Errorf("deploy 失败且读取失败节点失败: deployErr=%v, listErr=%w", deployErr, listErr)
		}
		return listErr
	}

	// 初次 deploy 无失败节点，直接返回原结果（通常为 nil）。
	if len(failedIPs) == 0 {
		return deployErr
	}

	log.Printf("⚠️ 检测到失败链，共 %d 条，开始按 deploy-restore 流程重试（最多 %d 次）: %s\n", len(failedIPs), deployRestoreMaxRetries, strings.Join(failedIPs, ", "))

	for attempt := 1; attempt <= deployRestoreMaxRetries; attempt++ {
		log.Printf("🔁 第 %d/%d 次失败链重试，目标 IP: %s\n", attempt, deployRestoreMaxRetries, strings.Join(failedIPs, ", "))

		if err := Restore(ctx, cfg.CommonConfig, failedIPs); err != nil {
			log.Printf("⚠️ 第 %d 次 deploy-restore 执行返回错误: %v\n", attempt, err)
		}

		failedIPs, listErr = listFailedIPsFromOutput(cfg.CommonConfig)
		if listErr != nil {
			return fmt.Errorf("第 %d 次重试后读取失败节点失败: %w", attempt, listErr)
		}
		if len(failedIPs) == 0 {
			log.Printf("✅ 第 %d 次重试后，所有链均成功\n", attempt)
			return nil
		}
	}

	return fmt.Errorf("deploy 完成后重试 %d 次仍有失败链，共 %d 条: %s", deployRestoreMaxRetries, len(failedIPs), strings.Join(failedIPs, ", "))
}

func listFailedIPsFromOutput(commonCfg CommonConfig) ([]string, error) {
	outputDir := commonCfg.OutputDir
	if outputDir == "" {
		outputDir = filepath.Join(commonCfg.LogDir, "output")
	}

	outputMgr, err := LoadOutputManager(outputDir)
	if err != nil {
		return nil, fmt.Errorf("加载输出状态失败: %w", err)
	}

	return selectFailedIPs(outputMgr.SnapshotStatuses()), nil
}

func selectFailedIPs(statuses []*ScriptStatus) []string {
	if len(statuses) == 0 {
		return nil
	}

	uniq := make(map[string]struct{}, len(statuses))
	for _, st := range statuses {
		if st == nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(st.Status), "failed") {
			continue
		}
		ip := strings.TrimSpace(st.IP)
		if ip == "" {
			continue
		}
		uniq[ip] = struct{}{}
	}

	if len(uniq) == 0 {
		return nil
	}

	ips := make([]string, 0, len(uniq))
	for ip := range uniq {
		ips = append(ips, ip)
	}
	sort.Strings(ips)
	return ips
}

func (d *Deployer) Run() error {
	if d == nil {
		return nil
	}

	log.Printf("👉 准备部署，为所有 L2 链的 L1 Valut 充值。\n 配置信息: %+v\n", d.cfg)
	if err := d.fundAllL1Vaults(); err != nil {
		return fmt.Errorf("为所有 L1 钱包充值失败: %w", err)
	}
	// return nil

	log.Println("👉 开始部署")

	for _, svc := range d.cfg.Services {
		if svc.Count <= 0 {
			continue
		}
		if err := d.runService(svc); err != nil {
			return err
		}
	}

	log.Println("👉 所有远程命令已启动，开始同步日志与脚本状态...")

	// 所有服务器上的脚本都已启动后，开始同步远端日志并同步到本地，同时更新脚本运行状态。
	s := NewSync(d.cfg.CommonConfig, d.outputMgr)
	if err := s.Run(d.ctx); err != nil {
		return err
	}

	log.Println("✅ 所有 service 执行完成！")
	return nil
}

func (d *Deployer) runService(svc ServiceConfig) error {
	target := int(svc.Count)
	log.Printf("👉 [%s] 目标可用机器数=%d，开始进行 SSH 可用性收敛...\n", svc.Type.String(), target)

	readyIPs, err := d.acquireSSHReadyIPs(svc, target)
	if err != nil {
		return err
	}
	log.Printf("✅ [%s] SSH 可用机器收敛完成，数量=%d，IP=%v\n", svc.Type.String(), len(readyIPs), readyIPs)

	// SSH 收敛完成后才构建 name/command，避免将不可 SSH 机器纳入后续流程。
	servers := make([]ServerInfo, 0, len(readyIPs))
	for idx, ip := range readyIPs {
		servers = append(servers, ServerInfo{
			IP:          ip,
			ServiceType: svc.Type.String(),
			Name:        d.buildInstanceName(svc.TagPrefix, svc.Type.String(), idx+1),
		})
	}
	if err := d.outputMgr.AddServers(servers); err != nil {
		log.Printf("写入服务器列表失败: %v\n", err)
	}

	log.Printf("👉 [%s] 预登记脚本状态（pending，可用于后续 restore）...\n", svc.Type.String())
	if err := d.preRegisterStatuses(readyIPs, svc); err != nil {
		return err
	}

	log.Printf("👉 [%s] 批量执行远程命令（后台）...\n", svc.Type.String())
	if err := d.runCommandsOnInstances(readyIPs, svc); err != nil {
		return err
	}

	return nil
}

func (d *Deployer) acquireSSHReadyIPs(svc ServiceConfig, target int) ([]string, error) {
	if d.reuseFromSnapshot {
		return d.acquireSSHReadyIPsFromSnapshot(svc, target)
	}

	nextCreateOrdinal := 1
	return d.acquireSSHReadyIPsWithProvider(svc, target, d.sshAcquireMaxRound(), func(need int, round int, svc ServiceConfig) ([]string, []string, error) {
		log.Printf("👉 [%s] 第 %d/%d 轮补机：需补 %d 台\n", svc.Type.String(), round, d.sshAcquireMaxRound(), need)
		batchSvc := svc
		batchSvc.Count = uint(need)

		launcher := NewEC2RunInstancesLauncher(d.ctx, d.ec2Client, d.cfg.CommonConfig, d.buildInstanceName)
		instanceIDs, err := launcher.RunWithStartOrdinal(batchSvc, nextCreateOrdinal)
		if err != nil {
			return nil, nil, err
		}
		nextCreateOrdinal += len(instanceIDs)
		log.Printf("[%s] 第 %d 轮实例 ID: %v\n", svc.Type.String(), round, instanceIDs)

		log.Printf("👉 [%s] 第 %d 轮等待实例进入 running 状态...\n", svc.Type.String(), round)
		if err := d.waitInstancesRunning(instanceIDs); err != nil {
			return nil, nil, err
		}

		log.Printf("👉 [%s] 第 %d 轮获取实例公网 IP...\n", svc.Type.String(), round)
		ips, err := d.getInstancePublicIPs(instanceIDs)
		if err != nil {
			return nil, nil, err
		}
		log.Printf("[%s] 第 %d 轮实例 IP: %v\n", svc.Type.String(), round, ips)
		createdServers := make([]CreatedServerInfo, 0, len(ips))
		for i, ip := range ips {
			createdServers = append(createdServers, CreatedServerInfo{
				Name:        buildCreateStageTagName(svc.TagPrefix, svc.Type.String(), nextCreateOrdinal-len(instanceIDs)+i),
				ServiceType: svc.Type.String(),
				IP:          ip,
			})
		}
		if err := d.outputMgr.AddCreatedServers(createdServers); err != nil {
			return nil, nil, fmt.Errorf("写入 servers_create.json 失败: %w", err)
		}

		log.Printf("👉 [%s] 第 %d 轮等待每台机器 SSH 就绪...\n", svc.Type.String(), round)
		return d.waitAllSSHReady(ips, svc)
	})
}

func (d *Deployer) acquireSSHReadyIPsFromSnapshot(svc ServiceConfig, target int) ([]string, error) {
	pool := d.snapshotIPPoolForService(svc.Type.String())
	if len(pool) < target {
		return nil, fmt.Errorf("[%s] servers_create 快照中可用 IP 不足: need=%d, got=%d", svc.Type.String(), target, len(pool))
	}

	log.Printf("👉 [%s] 复用 servers_create 快照，跳过 EC2 创建；候选 IP 数=%d，目标=%d\n", svc.Type.String(), len(pool), target)

	nextIdx := 0
	return d.acquireSSHReadyIPsWithProvider(svc, target, d.sshAcquireMaxRound(), func(need int, round int, svc ServiceConfig) ([]string, []string, error) {
		if nextIdx+need > len(pool) {
			return nil, nil, fmt.Errorf("[%s] servers_create 快照池已耗尽: need=%d, remaining=%d", svc.Type.String(), need, len(pool)-nextIdx)
		}
		batch := pool[nextIdx : nextIdx+need]
		nextIdx += need
		log.Printf("👉 [%s] 第 %d/%d 轮 SSH 探测：批量机器数=%d，IP=%v\n", svc.Type.String(), round, d.sshAcquireMaxRound(), len(batch), batch)
		return d.waitAllSSHReady(batch, svc)
	})
}

func (d *Deployer) snapshotIPPoolForService(wantType string) []string {
	want := strings.TrimSpace(wantType)
	seen := make(map[string]struct{})
	order := make([]string, 0, len(d.importedSnapshot))
	for _, c := range d.importedSnapshot {
		if !strings.EqualFold(strings.TrimSpace(c.ServiceType), want) {
			continue
		}
		ip := strings.TrimSpace(c.IP)
		if ip == "" {
			continue
		}
		if _, ok := seen[ip]; ok {
			continue
		}
		seen[ip] = struct{}{}
		order = append(order, ip)
	}
	sort.Strings(order)
	return order
}

func (d *Deployer) acquireSSHReadyIPsWithProvider(svc ServiceConfig, target, maxRound int, provider func(need int, round int, svc ServiceConfig) ([]string, []string, error)) ([]string, error) {
	if target <= 0 {
		return nil, nil
	}
	if maxRound <= 0 {
		maxRound = 1
	}

	readyIPs := make([]string, 0, target)
	failureRounds := make([]string, 0)

	for round := 1; round <= maxRound; round++ {
		need := target - len(readyIPs)
		if need <= 0 {
			break
		}
		successIPs, failedIPs, err := provider(need, round, svc)
		if err != nil {
			return nil, err
		}
		if len(failedIPs) > 0 {
			failureRounds = append(failureRounds, fmt.Sprintf("round=%d failed=%v", round, failedIPs))
			log.Printf("⚠️ [%s] 第 %d 轮 SSH 失败 %d 台，将按失败台数补机: %v\n", svc.Type.String(), round, len(failedIPs), failedIPs)
		}
		readyIPs = append(readyIPs, successIPs...)
		log.Printf("ℹ️ [%s] 第 %d 轮 SSH 成功 %d 台，累计可用 %d/%d\n", svc.Type.String(), round, len(successIPs), len(readyIPs), target)
	}

	if len(readyIPs) < target {
		errMsg := fmt.Sprintf("[%s] SSH 可用机器不足: target=%d, ready=%d, maxRound=%d", svc.Type.String(), target, len(readyIPs), maxRound)
		if len(failureRounds) > 0 {
			errMsg = fmt.Sprintf("%s, details=%s", errMsg, strings.Join(failureRounds, "; "))
		}
		return nil, errors.New(errMsg)
	}

	return readyIPs[:target], nil
}

func (d *Deployer) waitInstancesRunning(ids []*string) error {
	input := &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	}

	return d.ec2Client.WaitUntilInstanceRunningWithContext(d.ctx, input)
}

func (d *Deployer) getInstancePublicIPs(ids []*string) ([]string, error) {
	input := &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	}

	out, err := d.ec2Client.DescribeInstancesWithContext(d.ctx, input)
	if err != nil {
		return nil, fmt.Errorf("DescribeInstances 失败: %w", err)
	}

	var ips []string
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.PublicIpAddress != nil && *inst.PublicIpAddress != "" {
				ips = append(ips, *inst.PublicIpAddress)
			}
		}
	}

	if len(ips) == 0 {
		return nil, fmt.Errorf("未获取到任何实例公网 IP")
	}
	return ips, nil
}

func (d *Deployer) waitAllSSHReady(ips []string, svc ServiceConfig) ([]string, []string, error) {
	if len(ips) == 0 {
		return nil, nil, nil
	}

	var (
		mu         sync.Mutex
		errs       []error
		readyFlags = make([]bool, len(ips))
		failFlags  = make([]bool, len(ips))
	)

	addErr := func(ip string, err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		errs = append(errs, fmt.Errorf("[%s] %w", ip, err))
	}

	runWithBatchLimit("wait-ssh-ready", len(ips), d.sshMaxConcurrency(), func(i int) {
		ip := ips[i]
		log.Printf("[%s] 等待 SSH 就绪...\n", ip)

		attempts, err := d.waitSSHReadyWithRetry(ip)
		now := time.Now().Unix()
		if err != nil {
			failFlags[i] = true
			if persistErr := d.outputMgr.UpdateSSHScriptStatus(ip, svc.Type.String(), "", "fail", attempts, err.Error(), now); persistErr != nil {
				addErr(ip, fmt.Errorf("写入 ssh_scripts.json 失败: %w", persistErr))
			}
			return
		}
		readyFlags[i] = true

		if persistErr := d.outputMgr.UpdateSSHScriptStatus(ip, svc.Type.String(), "", "success", attempts, "", now); persistErr != nil {
			addErr(ip, fmt.Errorf("写入 ssh_scripts.json 失败: %w", persistErr))
		}
	})

	if len(errs) != 0 {
		return nil, nil, deployMultiError{errs: errs}
	}

	successIPs := make([]string, 0, len(ips))
	failedIPs := make([]string, 0, len(ips))
	for i, ip := range ips {
		if readyFlags[i] {
			successIPs = append(successIPs, ip)
			continue
		}
		if failFlags[i] {
			failedIPs = append(failedIPs, ip)
		}
	}

	return successIPs, failedIPs, nil
}

func (d *Deployer) runCommandsOnInstances(ips []string, svc ServiceConfig) error {
	var (
		mu   sync.Mutex
		errs []error
	)

	cfg := d.cfg.CommonConfig

	// 并发收集每台机器的错误，最终统一汇总返回（不再只返回“第一个错误”）。
	addErr := func(ip, name string, err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		// name 可能为空（极少数早期失败场景），统一格式化方便用户排查。
		if name != "" {
			errs = append(errs, fmt.Errorf("[%s][%s] %w", ip, name, err))
		} else {
			errs = append(errs, fmt.Errorf("[%s] %w", ip, err))
		}
	}

	runWithBatchLimit("run-remote-command", len(ips), d.sshMaxConcurrency(), func(idx int) {
		i := idx
		ip := ips[idx]
		name := d.buildInstanceName(svc.TagPrefix, svc.Type.String(), i+1)
		logPrefix := fmt.Sprintf("[%s][%s]", ip, name)
		log.Printf("%s 开始部署任务\n", logPrefix)

		// 再次确认标签（与 shell 版一致，用 ip -> instanceId -> 打 Name 标签）
		log.Printf("%s STEP1: 查询实例 ID...\n", logPrefix)
		instID, err := d.findInstanceByIP(ip)
		if err != nil {
			addErr(ip, name, err)
			return
		}
		log.Printf("%s STEP1: 查询实例 ID 完成，instanceId=%s\n", logPrefix, instID)

		log.Printf("%s STEP2: 设置实例 Name 标签...\n", logPrefix)
		if err := d.tagInstanceName(instID, name); err != nil {
			addErr(ip, name, err)
			return
		}
		log.Printf("%s STEP2: 设置实例 Name 标签完成\n", logPrefix)

		log.Printf("%s STEP3: 生成远端执行命令...\n", logPrefix)
		cmdStr, err := d.buildRemoteCommandForIndex(ips, i, svc)
		if err != nil {
			addErr(ip, name, err)
			return
		}
		log.Printf("%s STEP3: 生成远端执行命令完成\n", logPrefix)

		remoteLogFile, remoteLogDir := buildRemoteLogPath("", name)

		// 在远端后台运行脚本，并将 stdout/stderr 重定向到远端日志文件。
		// 同时输出子进程 PID，便于后续状态监控。
		log.Printf("%s STEP4: 构造远端后台运行命令...\n", logPrefix)
		fullCmd := buildBackgroundCommand(cfg.RunDuration, cmdStr, remoteLogDir, remoteLogFile)
		log.Printf("%s STEP4: 构造远端后台运行命令完成\n", logPrefix)

		log.Printf("%s run (background): %s\n", logPrefix, fullCmd)

		localLogPath := buildLocalLogPath(cfg.LogDir, ip, name)

		log.Printf("%s STEP5: 通过 ssh 启动远端后台任务...\n", logPrefix)
		sshCmd := exec.CommandContext(d.ctx, "ssh",
			"-o", "StrictHostKeyChecking=no",
			"-o", "IdentitiesOnly=yes",
			"-i", d.sshKeyPath,
			fmt.Sprintf("%s@%s", cfg.SSHUser, ip),
			fullCmd,
		)

		var stdoutBuf bytes.Buffer
		sshCmd.Stdout = &stdoutBuf
		sshCmd.Stderr = &stdoutBuf

		if err := sshCmd.Run(); err != nil {
			// 为了便于排查 ssh 相关问题（如 exit status 255），这里输出更详细的日志。
			if exitErr, ok := err.(*exec.ExitError); ok {
				// 注意：stderr 已经重定向到 logFile，这里只打印 exitCode 和命令本身。
				log.Printf("%s ssh 命令执行失败，exitCode=%d，cmd=%q\n", logPrefix, exitErr.ExitCode(), fullCmd)
				addErr(ip, name, fmt.Errorf("远程命令执行失败，exitCode=%d: %w", exitErr.ExitCode(), err))
			} else {
				log.Printf("%s ssh 命令执行失败（非 ExitError），cmd=%q，err=%v\n", logPrefix, fullCmd, err)
				addErr(ip, name, fmt.Errorf("远程命令执行失败: %w", err))
			}
			return
		}
		log.Printf("%s STEP5: ssh 启动远端后台任务完成\n", logPrefix)

		// 解析远端返回的 PID，用于后续状态监控
		log.Printf("%s STEP6: 解析远端 PID...\n", logPrefix)
		pid, parseErr := parseRemotePID(stdoutBuf.String())
		if parseErr != nil {
			// output 为空/非 PID 都属于异常情况：远端未按预期返回 PID，无法进行后续监控，直接判定失败。
			addErr(ip, name, fmt.Errorf("解析远端 PID 失败: %w，输出: %q", parseErr, stdoutBuf.String()))
			return
		}
		if pid <= 0 {
			addErr(ip, name, fmt.Errorf("任务执行失败，远端 PID 为 0，远端输出: %q", stdoutBuf.String()))
			return
		}

		log.Printf("%s STEP6: 解析远端 PID 完成，pid=%d\n", logPrefix, pid)

		// 初始化脚本运行状态
		log.Printf("%s STEP7: 初始化本地运行状态记录...\n", logPrefix)
		err = d.outputMgr.InitStatus(
			ip,
			svc.Type.String(),
			name,
			cmdStr,
			pid,
			remoteLogFile,
			localLogPath,
			time.Now().Unix(),
		)
		if err != nil {
			addErr(ip, name, err)
			return
		}
		log.Printf("%s STEP7: 初始化本地运行状态记录完成\n", logPrefix)
		log.Printf("%s 部署任务完成\n", logPrefix)
	})

	if len(errs) == 0 {
		return nil
	}

	// 汇总错误：每台机器一条，便于一次性定位问题。
	return deployMultiError{errs: errs}
}

func (d *Deployer) waitSSHReadyWithRetry(ip string) (uint, error) {
	return waitSSHReadyWithRetry(d.ctx, ip, d.cfg.CommonConfig.SSHUser, d.sshKeyPath, d.sshReadyRetryCount(), d.sshReadyRetryInterval())
}

func (d *Deployer) preRegisterStatuses(ips []string, svc ServiceConfig) error {
	var (
		mu   sync.Mutex
		errs []error
	)

	addErr := func(ip, name string, err error) {
		if err == nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if name != "" {
			errs = append(errs, fmt.Errorf("[%s][%s] %w", ip, name, err))
		} else {
			errs = append(errs, fmt.Errorf("[%s] %w", ip, err))
		}
	}

	runWithBatchLimit("preregister-script-status", len(ips), d.sshMaxConcurrency(), func(idx int) {
		i := idx
		ip := ips[idx]
		name := d.buildInstanceName(svc.TagPrefix, svc.Type.String(), i+1)

		cmdStr, err := d.buildRemoteCommandForIndex(ips, i, svc)
		if err != nil {
			addErr(ip, name, err)
			return
		}

		remoteLogFile, _ := buildRemoteLogPath("", name)
		localLogPath := buildLocalLogPath(d.cfg.CommonConfig.LogDir, ip, name)
		if err := d.outputMgr.UpsertPlannedStatus(
			ip,
			svc.Type.String(),
			name,
			cmdStr,
			remoteLogFile,
			localLogPath,
			time.Now().Unix(),
		); err != nil {
			addErr(ip, name, fmt.Errorf("预登记 script_status 失败: %w", err))
		}
	})

	if len(errs) == 0 {
		return nil
	}
	return deployMultiError{errs: errs}
}

func (d *Deployer) sshMaxConcurrency() int {
	return resolveSSHMaxConcurrency(d.cfg.CommonConfig)
}

func (d *Deployer) sshReadyRetryCount() uint {
	return resolveSSHReadyRetryCount(d.cfg.CommonConfig)
}

func (d *Deployer) sshReadyRetryInterval() time.Duration {
	return resolveSSHReadyRetryInterval(d.cfg.CommonConfig)
}

func (d *Deployer) sshAcquireMaxRound() int {
	return defaultSSHAcquireMaxRound
}

func runWithBatchLimit(taskName string, total, batchLimit int, task func(index int)) {
	if total <= 0 {
		return
	}
	if taskName == "" {
		taskName = "unnamed-task"
	}
	if batchLimit <= 0 {
		batchLimit = total
	}
	totalRounds := (total + batchLimit - 1) / batchLimit
	for start := 0; start < total; start += batchLimit {
		end := start + batchLimit
		if end > total {
			end = total
		}
		round := start/batchLimit + 1
		log.Printf("🧩 [batch:%s] 第 %d/%d 轮开始，index=[%d,%d]，batchLimit=%d\n", taskName, round, totalRounds, start, end-1, batchLimit)

		var wg sync.WaitGroup
		for i := start; i < end; i++ {
			idx := i
			roundNo := round
			wg.Add(1)
			go func(index, currentRound int) {
				defer wg.Done()
				globalSeq := index + 1
				log.Printf("▶️ [batch:%s][round:%d/%d][task:%d/%d] 开始\n", taskName, currentRound, totalRounds, globalSeq, total)
				defer log.Printf("✅ [batch:%s][round:%d/%d][task:%d/%d] 完成\n", taskName, currentRound, totalRounds, globalSeq, total)
				task(index)
			}(idx, roundNo)
		}
		wg.Wait()
	}
	log.Printf("📦 [batch:%s] 全部完成，共 %d 轮，task=%d\n", taskName, totalRounds, total)
}

func resolveSSHMaxConcurrency(cfg CommonConfig) int {
	v := int(cfg.SSHMaxConcurrency)
	if v <= 0 {
		return defaultSSHMaxConcurrency
	}
	return v
}

func resolveSSHReadyRetryCount(cfg CommonConfig) uint {
	if cfg.SSHReadyRetryCount == 0 {
		return defaultSSHReadyRetryCount
	}
	return cfg.SSHReadyRetryCount
}

func resolveSSHReadyRetryInterval(cfg CommonConfig) time.Duration {
	if cfg.SSHReadyRetryInterval <= 0 {
		return defaultSSHReadyRetryWait
	}
	return cfg.SSHReadyRetryInterval
}

func waitSSHReadyWithRetry(ctx context.Context, ip, sshUser, sshKeyPath string, retries uint, interval time.Duration) (uint, error) {
	totalAttempts := retries + 1
	var lastErr error
	for attempt := uint(1); attempt <= totalAttempts; attempt++ {
		err := waitSSHReadyFunc(ctx, ip, sshUser, sshKeyPath)
		if err == nil {
			return attempt, nil
		}
		lastErr = err
		if attempt == totalAttempts {
			break
		}
		log.Printf("[%s] SSH 未就绪，第 %d/%d 次失败，%s 后重试，err=%v\n", ip, attempt, totalAttempts, interval, err)
		select {
		case <-ctx.Done():
			return attempt, ctx.Err()
		case <-time.After(interval):
		}
	}

	return totalAttempts, fmt.Errorf("SSH 就绪探测失败（重试 %d 次）: %w", retries, lastErr)
}

func (d *Deployer) buildInstanceName(tagPrefix, serviceType string, ordinal int) string {
	if serviceType != enums.ServiceTypeXJST.String() {
		return fmt.Sprintf("%s-%s-%d", tagPrefix, serviceType, ordinal)
	}

	// ordinal 为 1-based，xjst 命名需要按每 4 个节点分组并输出组内序号 1~4。
	zeroIndex := ordinal - 1
	groupID := d.resolveXjstGroupId(zeroIndex)
	indexInGroup := zeroIndex%4 + 1
	return fmt.Sprintf("%s-%s-%d-%d", tagPrefix, serviceType, groupID, indexInGroup)
}

func buildSSHKeyPath(cfg CommonConfig) string {
	keyDir := cfg.SSHKeyDir
	if keyDir == "" {
		home, _ := os.UserHomeDir()
		keyDir = filepath.Join(home, ".ssh")
	}
	return filepath.Join(keyDir, cfg.KeyName+".pem")
}

func (d *Deployer) findInstanceByIP(ip string) (string, error) {
	out, err := d.ec2Client.DescribeInstancesWithContext(d.ctx, &ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{
			{
				Name:   aws.String("ip-address"),
				Values: []*string{aws.String(ip)},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("根据 IP=%s 查询实例失败: %w", ip, err)
	}

	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil {
				return *inst.InstanceId, nil
			}
		}
	}
	return "", fmt.Errorf("根据 IP=%s 未找到任何实例", ip)
}

func (d *Deployer) tagInstanceName(instanceID, name string) error {
	_, err := d.ec2Client.CreateTagsWithContext(d.ctx, &ec2.CreateTagsInput{
		Resources: []*string{aws.String(instanceID)},
		Tags: []*ec2.Tag{
			{
				Key:   aws.String("Name"),
				Value: aws.String(name),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("为实例 %s 设置 Name=%s 失败: %w", instanceID, name, err)
	}
	return nil
}

// buildRemoteCommandForIndex 根据索引与 service 策略生成远程命令。
// 当前实现：
//   - generic: 必须在配置中显式设置 remoteCmd，否则报错；
//   - op: 如果未配置 remoteCmd，则为每一台机器生成不同的 PRIVATE_KEY 和 L2_CHAIN_ID，
//     命令为：cd /home/ubuntu/op-work/scripts/deploy-op-stack && PRIVATE_KEY=<pk> L2_CHAIN_ID=<id> ./deploy-with-env.sh
//
// 后续可在此扩展 cdk / xjst 等模式。
func (d *Deployer) buildRemoteCommandForIndex(globalIps []string, i int, svc ServiceConfig) (string, error) {
	common := d.cfg.CommonConfig
	if svc.RemoteCmd != "" {
		return svc.RemoteCmd, nil
	}

	switch svc.Type {
	case enums.ServiceTypeGeneric:
		return "", fmt.Errorf("service=generic 时必须显式配置 remoteCmd")
	case enums.ServiceTypeOP:
		l2ChainID := d.resolveL2ChainID(svc.Type, i)
		l1VaultPrivateKey, err := d.resolveL1VaultPrivateKey(common.L1VaultMnemonic, svc.Type, l2ChainID)
		if err != nil {
			return "", fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
		}
		l1RpcUrl := d.resolveL1RpcUrl(common.L1RpcUrl, svc.L1RpcUrl)
		return fmt.Sprintf(
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive --force && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_HUB_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t ENABLE_GEN_ACC=%t ./op_pipe.sh",
			l2ChainID, common.L1ChainId, l1RpcUrl, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeHubContract, common.L1RegisterBridgePrivateKey, common.DryRun, common.ForceDeployL2Chain, common.EnableGenAccounts,
		), nil
	case enums.ServiceTypeCDK:
		// L2_CHAIN_ID=2025121101 L1_CHAIN_ID=3151908 L1_RPC_URL=https://eth.yidaiyilu0.site/rpc L1_VAULT_PRIVATE_KEY=0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59 L1_BRIDGE_HUB_CONTRACT=0x2634d61774eC4D4b721259e6ec2Ba1801733201C L1_REGISTER_BRIDGE_PRIVATE_KEY=0x9abda6411083c4e3391a7e93a9c1cfa6cf8364a04b44668854bb82c9d6d2dce0 DRYRUN=false FORCE_DEPLOY_CDK=false START_STEP=1 ./cdk_pipe.sh
		l2ChainID := d.resolveL2ChainID(svc.Type, i)
		l1VaultPrivateKey, err := d.resolveL1VaultPrivateKey(common.L1VaultMnemonic, svc.Type, l2ChainID)
		if err != nil {
			return "", fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
		}
		l1RpcUrl := d.resolveL1RpcUrl(common.L1RpcUrl, svc.L1RpcUrl)
		return fmt.Sprintf(
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive --force && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_HUB_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t ENABLE_GEN_ACC=%t ./cdk_pipe.sh",
			l2ChainID, common.L1ChainId, l1RpcUrl, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeHubContract, common.L1RegisterBridgePrivateKey, common.DryRun, common.ForceDeployL2Chain, common.EnableGenAccounts,
		), nil
	case enums.ServiceTypeXJST:
		groupId := d.resolveXjstGroupId(i)
		l1VaultPrivateKey, err := d.resolveL1VaultPrivateKey(common.L1VaultMnemonic, svc.Type, groupId)
		if err != nil {
			return "", fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
		}
		l1RpcUrl := d.resolveL1RpcUrl(common.L1RpcUrl, svc.L1RpcUrl)
		l1RpcUrlWs := common.L1RpcUrlWs
		// CHAIN_NODE_IPS='[44.252.111.46,44.247.52.12,54.245.12.147,44.249.51.138]' \
		// NODE_ID='node-1' \
		// GROUP_ID=1 \
		// L1_RPC_URL_WS='ws://47.243.70.39/ws' \
		// L1_RPC_URL='https://confura.yidaiyilu0.site/espace' \
		// AUTO_DEPLOY_L1_CONTRACTS='false' \
		// L2_CHAIN_ID=0 \
		// L1_CHAIN_ID=1025 \
		// L1_VAULT_PRIVATE_KEY='0xd01fd3d7fdcc808840d676f4cbff81af45b2641d414d7a00e25c7bf8cc6c7e97' \
		// L1_BRIDGE_HUB_CONTRACT='0xC6dC4E1a24df87e78Cc4c63C43bdb5c5d9b69a22' \
		// L1_REGISTER_BRIDGE_PRIVATE_KEY='0xa7c740e7475dc9af937574f95080df8c48ad1035a2cd53111c377b00f29a8fee' \
		// ./xjst_pipe.sh
		groupIpsStr, err := d.resolveXjstGroupIps(globalIps, groupId)
		if err != nil {
			return "", fmt.Errorf("解析 xjst 分组 IP 失败: %w", err)
		}
		nodeId := i%4 + 1

		return fmt.Sprintf(
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive --force && CHAIN_NODE_IPS='%s' NODE_ID='node-%d' GROUP_ID=%d L1_RPC_URL_WS='%s' L1_RPC_URL='%s' AUTO_DEPLOY_L1_CONTRACTS='false' L2_CHAIN_ID=0 L1_CHAIN_ID=%v L1_VAULT_PRIVATE_KEY='%s' L1_BRIDGE_HUB_CONTRACT='%s' L1_REGISTER_BRIDGE_PRIVATE_KEY='%s' ENABLE_GEN_ACC='%t' BRIDGE_GAS_PRICE=100000000000 ./xjst_pipe.sh",
			groupIpsStr, nodeId, groupId, l1RpcUrlWs, l1RpcUrl, common.L1ChainId, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeHubContract, common.L1RegisterBridgePrivateKey, common.EnableGenAccounts,
		), nil

	default:
		return "", fmt.Errorf("未知的 service 类型: %s", svc.Type.String())
	}
}

func (d *Deployer) resolveXjstGroupIps(globalIps []string, groupId int) (string, error) {
	if groupId <= 0 {
		return "", fmt.Errorf("groupId 必须 >= 1，当前为 %d", groupId)
	}
	start := (groupId - 1) * 4
	end := start + 4
	if start < 0 || end > len(globalIps) {
		return "", fmt.Errorf("xjst 分组 IP 越界: groupId=%d, start=%d, end=%d, total=%d", groupId, start, end, len(globalIps))
	}

	groupIps := globalIps[start:end]
	groupIpsStr := "[" + strings.Join(groupIps, ",") + "]"
	return groupIpsStr, nil
}

// 从 源L1Vault（L1VaultMnemonic /m/44/60/0/0/0） 分发 L1 eth 到所有 service 的 L1VaultPrivateKey 地址
func (d *Deployer) fundAllL1Vaults() error {
	sourceVaultPrivateKey, err := privatekeyhelper.NewFromMnemonic(d.cfg.CommonConfig.L1VaultMnemonic, 0, nil)
	if err != nil {
		return fmt.Errorf("生成源 L1Vault 私钥失败: %w", err)
	}

	targetAddrs := []common.Address{}
	targetAmounts := []*big.Int{}

	for _, service := range d.cfg.Services {
		if service.L1VaultFundAmount <= 0 {
			continue
		}

		for i := 0; i < int(service.Count); i++ {
			var index int

			if service.Type == enums.ServiceTypeXJST {
				if i%4 != 0 {
					continue
				}
				index = d.resolveXjstGroupId(i)
			} else {
				index = d.resolveL2ChainID(service.Type, i)
			}

			l1VaultPrivateKey, err := d.resolveL1VaultPrivateKey(d.cfg.CommonConfig.L1VaultMnemonic, service.Type, index)
			if err != nil {
				return fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
			}
			s := signers.NewPrivateKeySigner(l1VaultPrivateKey)
			targetAddrs = append(targetAddrs, s.Address())
			targetAmounts = append(targetAmounts, big.NewInt(1).Mul(big.NewInt(1e18), big.NewInt(service.L1VaultFundAmount)))
		}
	}

	sourceVaultSigner := signers.NewPrivateKeySigner(sourceVaultPrivateKey)
	l1Client, err := web3go.NewClientWithOption(d.cfg.CommonConfig.L1RpcUrl, web3go.ClientOption{
		SignerManager: signers.NewSignerManager([]interfaces.Signer{sourceVaultSigner}),
	})
	if err != nil {
		return fmt.Errorf("创建 L1 客户端失败: %w", err)
	}

	for i, targetVaultAddress := range targetAddrs {
		soureValutAddress := sourceVaultSigner.Address()
		value := hexutil.Big(*targetAmounts[i])

		err := commonutils.Retry(3, 1000, "发送交易", func() error {
			txHash, err := l1Client.Eth.SendTransactionByArgs(types.TransactionArgs{
				From:  &soureValutAddress,
				To:    &targetVaultAddress,
				Value: &value,
			})
			if err != nil {
				logrus.WithField("index", i).WithField("amount", value.ToInt()).WithField("from", soureValutAddress).WithField("to", targetVaultAddress).WithField("error", err).Error("发送交易失败")
				return err
			}
			logrus.WithField("index", i).WithField("amount", value.ToInt()).WithField("from", soureValutAddress).WithField("to", targetVaultAddress).WithField("txHash", txHash).Info("发送交易成功")

			if i == len(targetAddrs)-1 {
				// wait receipt
				logrus.Info("等待交易确认")
				for {
					receipt, err := l1Client.Eth.TransactionReceipt(txHash)
					if err != nil {
						return err
					}
					if receipt != nil {
						break
					}
					time.Sleep(1000 * time.Millisecond)
					fmt.Print(".")
				}
				fmt.Println()
			}
			return nil
		})

		if err != nil {
			return fmt.Errorf("发送交易[%d]失败: %w", i, err)
		}
	}

	logrus.WithField("total", len(targetAddrs)).Info("发送交易完成")

	return nil
}

// op/cdk index 为 chainID, xjst index 为 groupID
func (d *Deployer) resolveL1VaultPrivateKey(commonL1VaultMnemonic string, serviceType enums.ServiceType, index int) (*ecdsa.PrivateKey, error) {
	deriveRand := d.l1VaultDeriveRand
	derivePath := fmt.Sprintf("m/44'/60'/%d/%d", deriveRand, serviceType)
	l1VaultPrivateKey, err := privatekeyhelper.NewFromMnemonic(commonL1VaultMnemonic, index, &privatekeyhelper.MnemonicOption{
		BaseDerivePath: derivePath,
	})

	if err != nil {
		return nil, errors.WithMessagef(err, "根据助记词衍生私钥失败, 服务类型: %s, 链 ID: %d", serviceType, index)
	}
	logrus.WithField("deriveRand", deriveRand).
		WithField("derivePath", derivePath).
		WithField("index", index).
		WithField("serviceType", serviceType).
		WithField("privateKey", l1VaultPrivateKey).
		WithField("address", crypto.PubkeyToAddress(l1VaultPrivateKey.PublicKey)).
		Info("衍生私钥")
	return l1VaultPrivateKey, nil
}

func (d *Deployer) resolveL2ChainID(serviceType enums.ServiceType, index int) int {
	switch serviceType {
	case enums.ServiceTypeOP, enums.ServiceTypeCDK:
		return 10000 + index
	case enums.ServiceTypeXJST:
		return 0
	default:
		return 0
	}
}

func (d *Deployer) resolveXjstGroupId(index int) int {
	// index 为 0-based，groupId 统一改为 1-based。
	return index/4 + 1
}

func (d *Deployer) resolveL1RpcUrl(commonL1RpcUrl, svcL1RpcUrl string) string {
	l1RpcUrl := commonL1RpcUrl
	if svcL1RpcUrl != "" {
		l1RpcUrl = svcL1RpcUrl
	}
	return l1RpcUrl
}

// deployMultiError 汇总多台机器的部署错误（每台机器一条）。
// 该错误既便于用户一眼看到全部失败机器，也可通过 Unwrap() []error 做 errors.Is / errors.As。
type deployMultiError struct {
	errs []error
}

func (e deployMultiError) Error() string {
	if len(e.errs) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "共有 %d 台机器部署失败：\n", len(e.errs))
	for _, err := range e.errs {
		fmt.Fprintf(&b, "- %s\n", err.Error())
	}
	return strings.TrimRight(b.String(), "\n")
}

func (e deployMultiError) Unwrap() []error { return e.errs }

// parseRemotePID 从 ssh 返回的输出中解析出远端后台进程的 PID。
func parseRemotePID(output string) (int, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return 0, fmt.Errorf("PID 输出为空")
	}

	// ssh 返回中可能包含多行，比如 shutdown 的提示信息 + PID，我们取最后一行非空文本。
	lines := strings.Split(trimmed, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err == nil {
			return pid, nil
		}
	}

	return 0, fmt.Errorf("无法从输出中解析 PID: %q", output)
}

func resolveDeployArchiveTimestamp(outputDir, logDir string) (string, error) {
	if outputDir != "" {
		if info, rotatable, err := dirInfoIfRotatable(outputDir); err != nil {
			return "", err
		} else if rotatable {
			statusPath := filepath.Join(outputDir, "script_status.json")
			if stInfo, err := os.Stat(statusPath); err == nil {
				return stInfo.ModTime().Format("20060102-150405"), nil
			}
			return info.ModTime().Format("20060102-150405"), nil
		}
	}

	if logDir != "" {
		if info, rotatable, err := dirInfoIfRotatable(logDir); err != nil {
			return "", err
		} else if rotatable {
			return info.ModTime().Format("20060102-150405"), nil
		}
	}

	return "", nil
}

// rotateExistingDirWithTimestamp 如果指定目录已存在且非空，则将其重命名为 <dir>-<ts>。
// ts 为空时会自动基于目录 mtime 生成时间戳；为避免同秒冲突，目标已存在时会自动追加序号后缀。
func rotateExistingDirWithTimestamp(dir, ts string) (bool, error) {
	if dir == "" {
		return false, nil
	}

	info, rotatable, err := dirInfoIfRotatable(dir)
	if err != nil {
		return false, err
	}
	if !rotatable {
		return false, nil
	}

	if ts == "" {
		ts = info.ModTime().Format("20060102-150405")
	}
	base := fmt.Sprintf("%s-%s", dir, ts)
	target := base
	for i := 1; ; i++ {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			break
		} else if err != nil {
			return false, err
		}
		target = fmt.Sprintf("%s-%d", base, i)
	}

	if err := os.Rename(dir, target); err != nil {
		return false, err
	}

	log.Printf("ℹ️ 检测到已有目录 %s，已归档为 %s\n", dir, target)
	return true, nil
}

func dirInfoIfRotatable(dir string) (os.FileInfo, bool, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !info.IsDir() {
		return nil, false, fmt.Errorf("路径不是目录: %s", dir)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, false, err
	}
	if len(entries) == 0 {
		return info, false, nil
	}
	return info, true, nil
}
