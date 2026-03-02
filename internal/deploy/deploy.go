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
}

// NewDeployer 负责初始化一次部署执行所需的基础依赖（目录/输出管理/AWS client/SSH key 路径）。
func NewDeployer(ctx context.Context, cfg DeployConfig) (*Deployer, error) {
	if err := cfg.CheckValid(); err != nil {
		return nil, err
	}

	// 1) 准备日志目录
	if err := os.MkdirAll(cfg.CommonConfig.LogDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 2) 设置并创建输出目录，用于保存 servers.json / script_status.json
	if cfg.CommonConfig.OutputDir == "" {
		cfg.CommonConfig.OutputDir = filepath.Join(cfg.CommonConfig.LogDir, "output")
	}

	// 3) 归档旧 output，避免覆盖上次结果
	if err := rotateExistingOutputDir(cfg.CommonConfig.OutputDir); err != nil {
		return nil, fmt.Errorf("归档旧的输出目录失败: %w", err)
	}
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

	return &Deployer{
		ctx:               ctx,
		cfg:               cfg,
		ec2Client:         ec2Client,
		outputMgr:         outputMgr,
		sshKeyPath:        keyPath,
		l1VaultDeriveRand: deriveRand,
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
	d, err := NewDeployer(ctx, cfg)
	if err != nil {
		return err
	}
	return d.Run()
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

		log.Printf("👉 [%s] 正在启动 %d 台 EC2 实例...\n", svc.Type.String(), svc.Count)
		instanceIDs, err := d.runInstances(svc)
		if err != nil {
			return err
		}
		log.Printf("[%s] 实例 ID: %v\n", svc.Type.String(), instanceIDs)

		log.Printf("👉 [%s] 等待实例进入 running 状态...\n", svc.Type.String())
		if err := d.waitInstancesRunning(instanceIDs); err != nil {
			return err
		}

		log.Printf("👉 [%s] 获取实例公网 IP...\n", svc.Type.String())
		ips, err := d.getInstancePublicIPs(instanceIDs)
		if err != nil {
			return err
		}
		log.Printf("[%s] 实例 IP: %v\n", svc.Type.String(), ips)

		// 记录服务器信息到输出文件中（包含与实例 Name tag 一致的逻辑名称）。
		servers := make([]ServerInfo, 0, len(ips))
		for idx, ip := range ips {
			servers = append(servers, ServerInfo{
				IP:          ip,
				ServiceType: svc.Type.String(),
				Name:        d.buildInstanceName(svc.TagPrefix, svc.Type.String(), idx+1),
			})
		}
		if err := d.outputMgr.AddServers(servers); err != nil {
			log.Printf("写入服务器列表失败: %v\n", err)
		}

		log.Printf("👉 [%s] 等待每台机器 SSH 就绪...\n", svc.Type.String())
		if err := d.waitAllSSHReady(ips); err != nil {
			return err
		}

		log.Printf("👉 [%s] 批量执行远程命令（后台）...\n", svc.Type.String())
		if err := d.runCommandsOnInstances(ips, svc); err != nil {
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

func (d *Deployer) runInstances(svc ServiceConfig) ([]*string, error) {
	cfg := d.cfg
	ec2Client := d.ec2Client

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

	out, err := ec2Client.RunInstancesWithContext(d.ctx, input)
	if err != nil {
		return nil, fmt.Errorf("启动实例失败: %w", err)
	}

	ids := make([]*string, 0, len(out.Instances))
	for _, inst := range out.Instances {
		ids = append(ids, inst.InstanceId)
	}

	// 逐台实例追加/覆盖 Name 标签为 TAG-<service>-1...TAG-<service>-N
	for i, id := range ids {
		name := d.buildInstanceName(svc.TagPrefix, svc.Type.String(), i+1)
		_, err := ec2Client.CreateTagsWithContext(d.ctx, &ec2.CreateTagsInput{
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

func (d *Deployer) waitAllSSHReady(ips []string) error {
	for _, ip := range ips {
		log.Printf("[%s] 等待 SSH 就绪...\n", ip)
		if err := sshutil.WaitSSH(d.ctx, ip, d.cfg.CommonConfig.SSHUser, d.sshKeyPath); err != nil {
			return err
		}
	}
	return nil
}

func (d *Deployer) runCommandsOnInstances(ips []string, svc ServiceConfig) error {
	var (
		wg   sync.WaitGroup
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

	for idx, ip := range ips {
		i := idx // service 内部编号，从 0 开始
		wg.Add(1)

		go func(i int, ip string) {
			defer wg.Done()

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
		}(i, ip)
	}

	wg.Wait()
	if len(errs) == 0 {
		return nil
	}

	// 汇总错误：每台机器一条，便于一次性定位问题。
	return deployMultiError{errs: errs}
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
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive --force && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_HUB_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t ./op_pipe.sh",
			l2ChainID, common.L1ChainId, l1RpcUrl, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeHubContract, common.L1RegisterBridgePrivateKey, common.DryRun, common.ForceDeployL2Chain,
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
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive --force && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_HUB_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t ./cdk_pipe.sh",
			l2ChainID, common.L1ChainId, l1RpcUrl, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeHubContract, common.L1RegisterBridgePrivateKey, common.DryRun, common.ForceDeployL2Chain,
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
			" git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive --force && CHAIN_NODE_IPS='%s' NODE_ID='node-%d' GROUP_ID=%d L1_RPC_URL_WS='%s' L1_RPC_URL='%s' AUTO_DEPLOY_L1_CONTRACTS='false' L2_CHAIN_ID=0 L1_CHAIN_ID=%v L1_VAULT_PRIVATE_KEY='%s' L1_BRIDGE_HUB_CONTRACT='%s' L1_REGISTER_BRIDGE_PRIVATE_KEY='%s' ./xjst_pipe.sh",
			groupIpsStr, nodeId, groupId, l1RpcUrlWs, l1RpcUrl, common.L1ChainId, cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey), common.L1BridgeHubContract, common.L1RegisterBridgePrivateKey,
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
