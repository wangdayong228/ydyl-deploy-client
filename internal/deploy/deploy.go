package deploy

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/openweb3/go-sdk-common/privatekeyhelper"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
	"github.com/wangdayong228/ydyl-deploy-client/internal/cryptoutil"
	"github.com/wangdayong228/ydyl-deploy-client/internal/sshutil"
)

// Run 按照 DeployConfig 中的参数，完成一次完整的批量部署流程：
// 对每个 ServiceConfig：
// 1）批量创建对应数量的 EC2 实例；2）等待实例 running；3）获取公网 IP 并等待 SSH 就绪；
// 4）为每个实例构造远程命令并执行；5）收集日志与执行结果。
func Run(ctx context.Context, cfg DeployConfig) error {

	if err := os.MkdirAll(cfg.CommonConfig.LogDir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 设置并创建输出目录，用于保存 servers.json / script_status.json
	if cfg.CommonConfig.OutputDir == "" {
		cfg.CommonConfig.OutputDir = filepath.Join(cfg.CommonConfig.LogDir, "output")
	}

	// 如果本次运行前已经存在 output 目录，则先做一次简单的归档备份：
	//   output/        -> output-YYYYMMDD-HHMMSS/
	// 以免新的部署覆盖掉上一次的 servers.json / script_status.json。
	if err := rotateExistingOutputDir(cfg.CommonConfig.OutputDir); err != nil {
		return fmt.Errorf("归档旧的输出目录失败: %w", err)
	}
	if err := os.MkdirAll(cfg.CommonConfig.OutputDir, 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}

	outputMgr := NewOutputManager(cfg.CommonConfig.OutputDir)

	awsCfg := aws.Config{}
	if cfg.CommonConfig.Region != "" {
		awsCfg.Region = aws.String(cfg.CommonConfig.Region)
	}

	sess, err := session.NewSession(&awsCfg)
	if err != nil {
		return fmt.Errorf("创建 AWS Session 失败: %w", err)
	}
	ec2Client := ec2.New(sess)

	for _, svc := range cfg.Services {
		if svc.Count <= 0 {
			continue
		}

		log.Printf("👉 [%s] 正在启动 %d 台 EC2 实例...\n", svc.Type.String(), svc.Count)
		instanceIDs, err := runInstances(ctx, ec2Client, cfg, svc)
		if err != nil {
			return err
		}
		log.Printf("[%s] 实例 ID: %v\n", svc.Type.String(), instanceIDs)

		log.Printf("👉 [%s] 等待实例进入 running 状态...\n", svc.Type.String())
		if err := waitInstancesRunning(ctx, ec2Client, instanceIDs); err != nil {
			return err
		}

		log.Printf("👉 [%s] 获取实例公网 IP...\n", svc.Type.String())
		ips, err := getInstancePublicIPs(ctx, ec2Client, instanceIDs)
		if err != nil {
			return err
		}
		log.Printf("[%s] 实例 IP: %v\n", svc.Type.String(), ips)

		// 记录服务器 IP 列表到输出文件中
		if err := outputMgr.AddServers(ips, svc.Type.String()); err != nil {
			log.Printf("写入服务器列表失败: %v\n", err)
		}

		log.Printf("👉 [%s] 等待每台机器 SSH 就绪...\n", svc.Type.String())
		if err := waitAllSSHReady(ctx, ips, cfg); err != nil {
			return err
		}

		log.Printf("👉 [%s] 批量执行远程命令（后台）...\n", svc.Type.String())
		if err := runCommandsOnInstances(ctx, ec2Client, ips, cfg.CommonConfig, svc, outputMgr); err != nil {
			return err
		}
	}

	log.Println("👉 所有远程命令已启动，开始同步日志与脚本状态...")

	// 所有服务器上的脚本都已启动后，开始同步远端日志并同步到本地，同时更新脚本运行状态。
	s := NewSync(cfg.CommonConfig, outputMgr)
	if err := s.Run(ctx); err != nil {
		return err
	}

	log.Println("✅ 所有 service 执行完成！")
	return nil
}

func runInstances(ctx context.Context, ec2Client *ec2.EC2, cfg DeployConfig, svc ServiceConfig) ([]*string, error) {
	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(svc.AMI),
		InstanceType: aws.String(svc.InstanceType),
		MinCount:     aws.Int64(int64(svc.Count)),
		MaxCount:     aws.Int64(int64(svc.Count)),
		KeyName:      aws.String(cfg.CommonConfig.KeyName),
		SecurityGroupIds: []*string{
			aws.String(cfg.CommonConfig.SecurityGroupID),
		},
		InstanceInitiatedShutdownBehavior: aws.String("terminate"),
		TagSpecifications:                 []*ec2.TagSpecification{},
	}

	// 如果在 CommonConfig 中配置了磁盘大小，则为所有实例设置统一的根盘大小
	if cfg.CommonConfig.DiskSizeGiB > 0 {
		input.BlockDeviceMappings = []*ec2.BlockDeviceMapping{
			{
				// 大多数 Ubuntu / Amazon Linux AMI 的根盘设备名为 /dev/xvda，如不符合可改为对应值
				DeviceName: aws.String("/dev/sda1"),
				Ebs: &ec2.EbsBlockDevice{
					VolumeSize:          aws.Int64(cfg.CommonConfig.DiskSizeGiB),
					VolumeType:          aws.String("gp3"),
					DeleteOnTermination: aws.Bool(true),
				},
			},
		}
	}

	out, err := ec2Client.RunInstancesWithContext(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("启动实例失败: %w", err)
	}

	ids := make([]*string, 0, len(out.Instances))
	for _, inst := range out.Instances {
		ids = append(ids, inst.InstanceId)
	}

	// 逐台实例追加/覆盖 Name 标签为 TAG-<service>-1...TAG-<service>-N
	for i, id := range ids {
		name := fmt.Sprintf("%s-%s-%d", svc.TagPrefix, svc.Type.String(), i+1)
		_, err := ec2Client.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
			Resources: []*string{id},
			Tags: []*ec2.Tag{
				{
					Key:   aws.String("Name"),
					Value: aws.String(name),
				},
			},
		})
		if err != nil {
			return nil, fmt.Errorf("为实例 %s 打标签失败: %w", aws.StringValue(id), err)
		}
	}

	return ids, nil
}

func waitInstancesRunning(ctx context.Context, ec2Client *ec2.EC2, ids []*string) error {
	input := &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	}

	return ec2Client.WaitUntilInstanceRunningWithContext(ctx, input)
}

func getInstancePublicIPs(ctx context.Context, ec2Client *ec2.EC2, ids []*string) ([]string, error) {
	input := &ec2.DescribeInstancesInput{
		InstanceIds: ids,
	}

	out, err := ec2Client.DescribeInstancesWithContext(ctx, input)
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

func waitAllSSHReady(ctx context.Context, ips []string, cfg DeployConfig) error {
	sshKeyPath := buildSSHKeyPath(cfg.CommonConfig)
	for _, ip := range ips {
		log.Printf("[%s] 等待 SSH 就绪...\n", ip)
		if err := sshutil.WaitSSH(ctx, ip, cfg.CommonConfig.SSHUser, sshKeyPath); err != nil {
			return err
		}
	}
	return nil
}

func runCommandsOnInstances(ctx context.Context, ec2Client *ec2.EC2, ips []string, cfg CommonConfig, svc ServiceConfig, outputMgr *OutputManager) error {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		errs    []error
		keyPath = buildSSHKeyPath(cfg)
	)

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

	for idx, ip := range ips {
		i := idx + 1 // service 内部编号，从 1 开始
		wg.Add(1)

		go func(i int, ip string) {
			defer wg.Done()

			name := fmt.Sprintf("%s-%s-%d", svc.TagPrefix, svc.Type.String(), i)
			logPrefix := fmt.Sprintf("[%s][%s]", ip, name)
			log.Printf("%s 开始部署任务\n", logPrefix)

			// 再次确认标签（与 shell 版一致，用 ip -> instanceId -> 打 Name 标签）
			log.Printf("%s STEP1: 查询实例 ID...\n", logPrefix)
			instID, err := findInstanceByIP(ctx, ec2Client, ip)
			if err != nil {
				addErr(ip, name, err)
				return
			}
			log.Printf("%s STEP1: 查询实例 ID 完成，instanceId=%s\n", logPrefix, instID)

			log.Printf("%s STEP2: 设置实例 Name 标签...\n", logPrefix)
			if err := tagInstanceName(ctx, ec2Client, instID, name); err != nil {
				addErr(ip, name, err)
				return
			}
			log.Printf("%s STEP2: 设置实例 Name 标签完成\n", logPrefix)

			log.Printf("%s STEP3: 生成远端执行命令...\n", logPrefix)
			cmdStr, err := buildRemoteCommandForIndex(i, svc, cfg)
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
			sshCmd := exec.CommandContext(ctx, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "IdentitiesOnly=yes",
				"-i", keyPath,
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
			log.Printf("%s STEP6: 解析远端 PID 完成，pid=%d\n", logPrefix, pid)

			// 初始化脚本运行状态
			log.Printf("%s STEP7: 初始化本地运行状态记录...\n", logPrefix)
			err = outputMgr.InitStatus(
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
		}(i, ip)
	}

	wg.Wait()
	if len(errs) == 0 {
		return nil
	}

	// 汇总错误：每台机器一条，便于一次性定位问题。
	return deployMultiError{errs: errs}
}

func buildSSHKeyPath(cfg CommonConfig) string {
	keyDir := cfg.SSHKeyDir
	if keyDir == "" {
		home, _ := os.UserHomeDir()
		keyDir = filepath.Join(home, ".ssh")
	}
	return filepath.Join(keyDir, cfg.KeyName+".pem")
}

func findInstanceByIP(ctx context.Context, ec2Client *ec2.EC2, ip string) (string, error) {
	out, err := ec2Client.DescribeInstancesWithContext(ctx, &ec2.DescribeInstancesInput{
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

func tagInstanceName(ctx context.Context, ec2Client *ec2.EC2, instanceID, name string) error {
	_, err := ec2Client.CreateTagsWithContext(ctx, &ec2.CreateTagsInput{
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
func buildRemoteCommandForIndex(i int, svc ServiceConfig, common CommonConfig) (string, error) {
	if svc.RemoteCmd != "" {
		return svc.RemoteCmd, nil
	}

	switch svc.Type {
	case enums.ServiceTypeGeneric:
		return "", fmt.Errorf("service=generic 时必须显式配置 remoteCmd")
	case enums.ServiceTypeOP:
		l2ChainID := 10000 + i
		l1VaultPrivateKey, err := privatekeyhelper.NewFromMnemonic(common.L1VaultMnemonic, i, nil)
		if err != nil {
			return "", fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
		}
		return fmt.Sprintf(
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_RELAY_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t ./op_pipe.sh",
			l2ChainID, common.L1ChainId, common.L1RpcUrl, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeRelayContract, common.L1RegisterBridgePrivateKey, common.DryRun, common.ForceDeployL2Chain,
		), nil
	case enums.ServiceTypeCDK:
		// L2_CHAIN_ID=2025121101 L1_CHAIN_ID=3151908 L1_RPC_URL=https://eth.yidaiyilu0.site/rpc L1_VAULT_PRIVATE_KEY=0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59 L1_BRIDGE_RELAY_CONTRACT=0x2634d61774eC4D4b721259e6ec2Ba1801733201C L1_REGISTER_BRIDGE_PRIVATE_KEY=0x9abda6411083c4e3391a7e93a9c1cfa6cf8364a04b44668854bb82c9d6d2dce0 DRYRUN=false FORCE_DEPLOY_CDK=false START_STEP=1 ./cdk_pipe.sh
		l2ChainID := 10000 + i
		l1VaultPrivateKey, err := privatekeyhelper.NewFromMnemonic(common.L1VaultMnemonic, i, nil)
		if err != nil {
			return "", fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
		}

		return fmt.Sprintf(
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_RELAY_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t ./cdk_pipe.sh",
			l2ChainID, common.L1ChainId, common.L1RpcUrl, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeRelayContract, common.L1RegisterBridgePrivateKey, common.DryRun, common.ForceDeployL2Chain,
		), nil
	case enums.ServiceTypeXJST:
		return "", fmt.Errorf("service=xjst 时必须显式配置 remoteCmd")
	default:
		return "", fmt.Errorf("未知的 service 类型: %s", svc.Type.String())
	}
}

// mkPrivateKeyHex 将整数转换为 64 位十六进制前缀 0x，模拟 shell 中的 mk_pk。
func mkPrivateKeyHex(i int) (string, error) {
	if i <= 0 {
		return "", fmt.Errorf("索引必须从 1 开始")
	}
	n := big.NewInt(int64(i))
	return fmt.Sprintf("0x%064x", n), nil
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

// rotateExistingOutputDir 如果指定的 output 目录已存在且非空，则将其重命名为 output-YYYYMMDD-HHMMSS。
// 时间戳优先使用旧的 script_status.json 的修改时间（近似代表上一次部署结束时间），否则退回当前时间。
// 用于在每次新的 deploy 前，对上一次的输出做一个简单归档，避免被覆盖。
func rotateExistingOutputDir(outputDir string) error {
	if outputDir == "" {
		return nil
	}

	info, err := os.Stat(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("outputDir 不是目录: %s", outputDir)
	}

	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		// 空目录，无需归档
		return nil
	}

	// 尝试用 script_status.json 的修改时间作为时间戳（更接近上一次运行的结束时间）
	var tsTime time.Time
	statusPath := filepath.Join(outputDir, "script_status.json")
	if stInfo, err := os.Stat(statusPath); err == nil {
		tsTime = stInfo.ModTime()
	} else {
		// 若不存在 script_status.json，则退回到目录本身的 mtime
		tsTime = info.ModTime()
	}
	ts := tsTime.Format("20060102-150405")
	newPath := fmt.Sprintf("%s-%s", outputDir, ts)

	if err := os.Rename(outputDir, newPath); err != nil {
		return fmt.Errorf("重命名输出目录失败: %w", err)
	}

	log.Printf("ℹ️ 检测到已有输出目录 %s，已归档为 %s\n", outputDir, newPath)
	return nil
}
