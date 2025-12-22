package deploy

import (
	"fmt"
	"path/filepath"
	"time"
)

// buildRemoteLogPath 返回远端日志文件路径及其目录。
// existingPath 为空时使用默认目录与 name 生成；非空时沿用原路径并取其所在目录。
func buildRemoteLogPath(existingPath, name string) (remoteLogFile, remoteLogDir string) {
	remoteLogDir = "/home/ubuntu/ydyl-deploy-logs"
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
		"sudo -n shutdown -h +%d; mkdir -p %s; cd /home/ubuntu/workspace/ydyl-deployment-suite; nohup %s > %s 2>&1 & echo $!",
		int(runDuration.Minutes()),
		remoteLogDir,
		cmd,
		remoteLogFile,
	)
}

// buildLocalLogPath 构造本地日志文件路径。
func buildLocalLogPath(logDir, ip, name string) string {
	return filepath.Join(logDir, fmt.Sprintf("%s-%s.log", ip, name))
}


