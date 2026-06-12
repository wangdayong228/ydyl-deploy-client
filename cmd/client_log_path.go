package cmd

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func clientLogPath(logDir, prefix string) string {
	if strings.TrimSpace(logDir) == "" {
		logDir = "logs"
	}
	return filepath.Join(
		logDir,
		"client",
		fmt.Sprintf("%s-%s.log", prefix, time.Now().UTC().Format("20060102-150405")),
	)
}
