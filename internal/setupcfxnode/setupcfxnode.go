package setupcfxnode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/openweb3/go-sdk-common/privatekeyhelper"
	"github.com/wangdayong228/ydyl-deploy-client/internal/deploy"
	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/commonutil"
	"github.com/wangdayong228/ydyl-deploy-client/internal/utils/cryptoutil"
)

type Params struct {
	ConfigPath string
}

type Setup struct {
	Runner oscmdexec.Runner
}

func NewSetup(runner oscmdexec.Runner) *Setup {
	return &Setup{Runner: runner}
}

func DefaultSetup() *Setup {
	return &Setup{Runner: oscmdexec.DefaultRunner}
}

func (s *Setup) Run(ctx context.Context, p Params) error {
	cfg := deploy.LoadConfigFromFile(p.ConfigPath)

	l1RPCURL := strings.TrimSpace(cfg.CommonConfig.L1RpcUrl)
	if l1RPCURL == "" {
		return fmt.Errorf("配置项 l1RpcUrl 不能为空")
	}

	l1VaultMnemonic := strings.TrimSpace(cfg.CommonConfig.L1VaultMnemonic)
	if l1VaultMnemonic == "" {
		return fmt.Errorf("配置项 l1VaultMnemonic 不能为空")
	}

	// 这里 index=0 + 默认路径，即 m/44'/60'/0'/0/0。
	l1VaultPrivateKey, err := privatekeyhelper.NewFromMnemonic(l1VaultMnemonic, 0, nil)
	if err != nil {
		return fmt.Errorf("根据 l1VaultMnemonic 派生私钥失败: %w", err)
	}

	env := commonutil.EnvOverride(os.Environ(), "L1_RPC_URL", l1RPCURL)
	env = commonutil.EnvOverride(env, "L1_VAULT_PRIVATE_KEY", cryptoutil.EcdsaPrivToWeb3Hex(l1VaultPrivateKey))

	scriptPath, err := resolveScriptPath()
	if err != nil {
		return err
	}

	spec := oscmdexec.Spec{
		Name: "bash",
		Args: []string{scriptPath},
		Env:  env,
	}

	runner := s.Runner
	if runner == nil {
		runner = oscmdexec.DefaultRunner
	}
	return runner(ctx, spec)
}

func resolveScriptPath() (string, error) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("获取当前文件路径失败")
	}
	// setupcfxnode.go 所在目录: ydyl-deploy-client/internal/setupcfxnode
	// 退三级到仓库根目录，再拼接 setup-cfxnode.sh
	scriptPath := filepath.Join(filepath.Dir(currentFile), "..", "..", "..", "setup-cfxnode.sh")
	absPath, err := filepath.Abs(scriptPath)
	if err != nil {
		return "", fmt.Errorf("解析 setup-cfxnode.sh 绝对路径失败: %w", err)
	}
	return filepath.Clean(absPath), nil
}
