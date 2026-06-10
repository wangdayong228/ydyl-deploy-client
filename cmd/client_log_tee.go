package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

func withClientCommandTee(logPath string, run func() error) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return fmt.Errorf("创建客户端日志目录失败: %w", err)
	}

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("打开客户端日志文件失败: %w", err)
	}
	defer logFile.Close()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	oldLogWriter := log.Writer()

	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("创建 stdout pipe 失败: %w", err)
	}
	defer stdoutR.Close()
	defer stdoutW.Close()

	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("创建 stderr pipe 失败: %w", err)
	}
	defer stderrR.Close()
	defer stderrW.Close()

	os.Stdout = stdoutW
	os.Stderr = stderrW
	log.SetOutput(stderrW)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.MultiWriter(oldStdout, logFile), stdoutR)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(io.MultiWriter(oldStderr, logFile), stderrR)
	}()

	runErr := run()

	_ = stdoutW.Close()
	_ = stderrW.Close()
	wg.Wait()

	os.Stdout = oldStdout
	os.Stderr = oldStderr
	log.SetOutput(oldLogWriter)

	return runErr
}
