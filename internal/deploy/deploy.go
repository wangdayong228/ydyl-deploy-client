package deploy

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

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

		log.Printf("👉 [%s] 等待每台机器 SSH 就绪...\n", svc.Type.String())
		if err := waitAllSSHReady(ctx, ips, cfg); err != nil {
			return err
		}

		log.Printf("👉 [%s] 批量执行远程命令...\n", svc.Type.String())
		if err := runCommandsOnInstances(ctx, ec2Client, ips, cfg.CommonConfig, svc); err != nil {
			return err
		}
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

func runCommandsOnInstances(ctx context.Context, ec2Client *ec2.EC2, ips []string, cfg CommonConfig, svc ServiceConfig) error {
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		first   error
		keyPath = buildSSHKeyPath(cfg)
	)

	for idx, ip := range ips {
		i := idx + 1 // service 内部编号，从 1 开始
		wg.Add(1)

		go func(i int, ip string) {
			defer wg.Done()

			name := fmt.Sprintf("%s-%s-%d", svc.TagPrefix, svc.Type.String(), i)

			// 再次确认标签（与 shell 版一致，用 ip -> instanceId -> 打 Name 标签）
			instID, err := findInstanceByIP(ctx, ec2Client, ip)
			if err != nil {
				setFirstErr(&mu, &first, err)
				return
			}
			if err := tagInstanceName(ctx, ec2Client, instID, name); err != nil {
				setFirstErr(&mu, &first, err)
				return
			}

			cmdStr, err := buildRemoteCommandForIndex(i, svc, cfg)
			if err != nil {
				setFirstErr(&mu, &first, err)
				return
			}

			fullCmd := fmt.Sprintf("sudo -n shutdown -h +%d && %s", int(cfg.RunDuration.Minutes()), cmdStr)
			log.Printf("[%s] run: %s\n", ip, fullCmd)

			logFilePath := filepath.Join(cfg.LogDir, fmt.Sprintf("%s-%s.log", ip, name))
			logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
			if err != nil {
				setFirstErr(&mu, &first, fmt.Errorf("打开日志文件失败 %s: %w", logFilePath, err))
				return
			}
			defer logFile.Close()

			sshCmd := exec.CommandContext(ctx, "ssh",
				"-o", "StrictHostKeyChecking=no",
				"-o", "IdentitiesOnly=yes",
				"-i", keyPath,
				fmt.Sprintf("%s@%s", cfg.SSHUser, ip),
				fullCmd,
			)

			sshCmd.Stdout = logFile
			sshCmd.Stderr = logFile

			if err := sshCmd.Run(); err != nil {
				setFirstErr(&mu, &first, fmt.Errorf("[%s] 远程命令执行失败: %w", ip, err))
				return
			}
		}(i, ip)
	}

	wg.Wait()
	return first
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
		pk, err := mkPrivateKeyHex(i)
		if err != nil {
			return "", err
		}
		chainID := 10000 + i
		return fmt.Sprintf(
			"cd /home/ubuntu/op-work/scripts/deploy-op-stack && PRIVATE_KEY=%s L2_CHAIN_ID=%d ./deploy-with-env.sh",
			pk, chainID,
		), nil
	case enums.ServiceTypeCDK:
		// L2_CHAIN_ID=2025121101 L1_CHAIN_ID=3151908 L1_RPC_URL=https://eth.yidaiyilu0.site/rpc L1_VAULT_PRIVATE_KEY=0x04b9f63ecf84210c5366c66d68fa1f5da1fa4f634fad6dfc86178e4d79ff9e59 L1_BRIDGE_RELAY_CONTRACT=0x2634d61774eC4D4b721259e6ec2Ba1801733201C L1_REGISTER_BRIDGE_PRIVATE_KEY=0x9abda6411083c4e3391a7e93a9c1cfa6cf8364a04b44668854bb82c9d6d2dce0 DRYRUN=false FORCE_DEPLOY_CDK=false START_STEP=1 ./cdk_pipe.sh
		l2ChainID := 10000 + i
		l1VaultPrivateKey, err := privatekeyhelper.NewFromMnemonic(common.L1VaultMnemonic, i, nil)
		if err != nil {
			return "", fmt.Errorf("生成 L1_VAULT_PRIVATE_KEY 失败: %w", err)
		}

		return fmt.Sprintf(
			"cd /home/ubuntu/workspace/ydyl-deployment-suite && git pull && GIT_SSH_COMMAND='ssh -o StrictHostKeyChecking=no' git submodule update --init --recursive && L2_CHAIN_ID=%d L1_CHAIN_ID=%v L1_RPC_URL=%s L1_VAULT_PRIVATE_KEY=%s L1_BRIDGE_RELAY_CONTRACT=%s L1_REGISTER_BRIDGE_PRIVATE_KEY=%s DRYRUN=%t FORCE_DEPLOY_CDK=%t START_STEP=1 ./cdk_pipe.sh",
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

func setFirstErr(mu *sync.Mutex, first *error, err error) {
	if err == nil {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if *first == nil {
		*first = err
	}
}
