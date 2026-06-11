package deploy

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/wangdayong228/ydyl-deploy-client/internal/constants/enums"
)

const (
	remoteLogDirDefault              = "/home/ubuntu/ydyl-deploy-logs"
	remoteRepoDirDefault             = "/home/ubuntu/workspace/ydyl-deployment-suite"
	remoteMonitorScriptMissingMarker = "__MONITOR_SCRIPT_MISSING__"
	remoteMonitorExitEarlyMarker     = "__MONITOR_EXIT_EARLY__"
)

// buildRemoteLogPath 返回远端日志文件路径及其目录。
// existingPath 为空时使用默认目录与 name 生成；非空时沿用原路径并取其所在目录。
func buildRemoteLogPath(existingPath, name string) (remoteLogFile, remoteLogDir string) {
	remoteLogDir = remoteLogDirDefault
	remoteLogFile = existingPath
	if remoteLogFile == "" {
		remoteLogFile = fmt.Sprintf("%s/%s.log", remoteLogDir, name)
	} else {
		remoteLogDir = filepath.Dir(remoteLogFile)
	}
	return remoteLogFile, remoteLogDir
}

// buildBackgroundCommand 根据原始 cmd 构造带有 shutdown/nohup 包装的完整命令。
func buildBackgroundCommand(runDuration time.Duration, cmd, remoteLogDir, remoteLogFile string) string {
	return fmt.Sprintf(
		"sudo -n shutdown -h +%d; mkdir -p %s; cd %s; nohup %s > %s 2>&1 & echo $!",
		int(runDuration.Minutes()),
		remoteLogDir,
		remoteRepoDirDefault,
		cmd,
		remoteLogFile,
	)
}

// buildLocalLogPath 构造本地日志文件路径。
func buildLocalLogPath(logDir, ip, name string) string {
	return filepath.Join(logDir, fmt.Sprintf("%s-%s.log", name, ip))
}

func buildRuntimeLogPath(name string) string {
	return fmt.Sprintf("%s/%s-runtime.log", remoteLogDirDefault, name)
}

func buildRuntimeMonitorCommand(serviceType enums.ServiceType, index int, name string) (string, bool) {
	output := buildRuntimeLogPath(name)
	scriptPath := fmt.Sprintf("%s/ydyl-scripts-lib/log_monitor_runtime.sh", remoteRepoDirDefault)
	buildStartCmd := func(args string) string {
		return fmt.Sprintf(
			"mkdir -p %s; cd %s; for i in $(seq 1 180); do [ -f %s ] && break; sleep 2; done; [ -f %s ] || { echo %s; exit 1; }; nohup bash %s %s >/dev/null 2>&1 & pid=$!; sleep 1; kill -0 \"$pid\" >/dev/null 2>&1 || { echo %s; exit 1; }; echo \"$pid\"",
			remoteLogDirDefault,
			remoteRepoDirDefault,
			scriptPath,
			scriptPath,
			remoteMonitorScriptMissingMarker,
			scriptPath,
			args,
			remoteMonitorExitEarlyMarker,
		)
	}

	switch serviceType {
	case enums.ServiceTypeCDK:
		return buildStartCmd(fmt.Sprintf("--mode kurtosis --stack cdk --output '%s' --enclave cdk-gen", output)), true
	case enums.ServiceTypeOP:
		return buildStartCmd(fmt.Sprintf("--mode kurtosis --stack op --output '%s' --enclave op-gen", output)), true
	case enums.ServiceTypeXJST:
		if index%4 != 0 {
			return "", false
		}
		return buildStartCmd(fmt.Sprintf("--mode docker --output '%s' --container testchain_node1", output)), true
	default:
		return "", false
	}
}
