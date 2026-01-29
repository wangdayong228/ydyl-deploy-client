package deploy

import (
	"errors"
	"time"

	"github.com/nft-rainbow/rainbow-goutils/utils/configutils"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
)

// ServiceConfig 描述单个 service 的配置
// AMI / 实例类型 / KeyName / 标签等都在 service 级别配置，保持模型简单。
type ServiceConfig struct {
	Type enums.ServiceType `yaml:"type"`

	AMI          string `yaml:"ami"`
	InstanceType string `yaml:"instanceType"`

	TagPrefix string `yaml:"tagPrefix"`

	Count     uint   `yaml:"count"`
	RemoteCmd string `yaml:"remoteCmd"`

	L1RpcUrl          string `yaml:"l1RpcUrl"`
	L1VaultFundAmount int64  `yaml:"l1VaultFundAmount"` // 单位：ether
}

func (s *ServiceConfig) CheckValid() error {
	if s.Type == enums.ServiceTypeXJST && s.Count%4 != 0 {
		return errors.New("xjst service count must be divisible by 4")
	}
	return nil
}

type CommonConfig struct {
	// AWS / EC2 相关（全局）
	Region          string `yaml:"region"`
	SecurityGroupID string `yaml:"securityGroupId"`
	// 所有实例的系统盘大小（GiB）
	DiskSizeGiB int64 `yaml:"diskSizeGiB"`

	// 运行与 SSH（全局）
	RunDuration time.Duration `yaml:"runDuration"`
	SSHUser     string        `yaml:"sshUser"`
	SSHKeyDir   string        `yaml:"sshKeyDir"` // 为空时默认使用 $HOME/.ssh
	KeyName     string        `yaml:"keyName"`
	LogDir      string        `yaml:"logDir"`

	// 输出目录：用于保存服务器 IP 列表和脚本运行状态等 JSON 文件
	OutputDir string `yaml:"outputDir"`

	// 链通用配置
	L1ChainId                  string `yaml:"l1ChainId"`
	L1RpcUrl                   string `yaml:"l1RpcUrl"`
	L1RpcUrlWs                 string `yaml:"l1RpcUrlWs"`
	L1VaultMnemonic            string `yaml:"l1VaultMnemonic"`
	L1BridgeHubContract        string `yaml:"l1BridgeHubContract"`
	L1RegisterBridgePrivateKey string `yaml:"l1RegisterBridgePrivateKey"`
	DryRun                     bool   `yaml:"dryRun"`
	ForceDeployL2Chain         bool   `yaml:"forceDeployL2Chain"`
}

// DeployConfig 描述一次 deploy 命令所需的全部参数
type DeployConfig struct {
	CommonConfig `yaml:",inline" mapstructure:",squash"`
	// 多个 service，每个 service 的数量和命令独立配置
	Services []ServiceConfig `yaml:"services"`
}

func (c *DeployConfig) CheckValid() error {
	for _, s := range c.Services {
		if err := s.CheckValid(); err != nil {
			return err
		}
	}
	return nil
}

// LoadConfigFromFile 从 YAML 文件加载配置并转换为内部 DeployConfig 结构
func LoadConfigFromFile(path string) *DeployConfig {
	return configutils.MustLoadByFile[DeployConfig](path)
}
