package deploy

import (
	"time"

	"github.com/nft-rainbow/rainbow-goutils/utils/configutils"
	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
)

// ServiceConfig 描述单个 service 的配置
// 每个 service 都有独立的 count / remoteCmd
type ServiceConfig struct {
	Type      enums.ServiceType
	Count     int
	RemoteCmd string
}

type BaseConfig struct {
	// AWS / EC2 相关
	Region          string
	AMI             string
	InstanceType    string
	KeyName         string
	SecurityGroupID string
	TagPrefix       string

	// 运行与 SSH
	RunDuration time.Duration
	SSHUser     string
	SSHKeyDir   string // 为空时默认使用 $HOME/.ssh
	LogDir      string
}

// DeployConfig 描述一次 deploy 命令所需的全部参数
type DeployConfig struct {
	// 多个 service，每个 service 的数量和命令独立配置
	Services []ServiceConfig
	BaseConfig
}

// // fileConfig / fileServiceConfig 仅用于反序列化 YAML 配置文件，保持对外 Config 结构简洁
// type fileServiceConfig struct {
// 	Type      string `yaml:"type"`
// 	Count     int    `yaml:"count"`
// 	RemoteCmd string `yaml:"remote_cmd"`
// }

// type fileConfig struct {
// 	Region          string              `yaml:"region"`
// 	AMI             string              `yaml:"ami"`
// 	InstanceType    string              `yaml:"instance_type"`
// 	KeyName         string              `yaml:"key_name"`
// 	SecurityGroupID string              `yaml:"security_group_id"`
// 	TagPrefix       string              `yaml:"tag_prefix"`
// 	RunMinutes      int                 `yaml:"run_minutes"`
// 	SSHUser         string              `yaml:"ssh_user"`
// 	SSHKeyDir       string              `yaml:"ssh_key_dir"`
// 	LogDir          string              `yaml:"log_dir"`
// 	Services        []fileServiceConfig `yaml:"services"`
// }

// LoadConfigFromFile 从 YAML 文件加载配置并转换为内部 Config 结构
func LoadConfigFromFile(path string) *DeployConfig {
	return configutils.MustLoadByFile[DeployConfig](path)
}
