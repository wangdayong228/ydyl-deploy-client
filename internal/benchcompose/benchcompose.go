package benchcompose

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/wangdayong228/ydyl-deploy-client/internal/infra/oscmdexec"
)

const (
	allJobsRelPath  = "output/jobs/all.json"
	benchDockerDir  = "ydyl-bench-docker"
	deployClientDir = "ydyl-deploy-client"
)

type Paths struct {
	DeployClientDir string
	AllJobsPath     string
	ComposeDir      string
}

func ResolvePaths() (Paths, error) {
	wd, err := os.Getwd()
	if err != nil {
		return Paths{}, fmt.Errorf("获取当前目录失败: %w", err)
	}

	for _, candidate := range deployClientCandidates(wd) {
		paths := buildPaths(candidate)
		if isDir(paths.DeployClientDir) && isDir(paths.ComposeDir) {
			return paths, nil
		}
	}

	return Paths{}, fmt.Errorf("未找到 ydyl-deploy-client 与 sibling ydyl-bench-docker（请在套件根目录或 ydyl-deploy-client 目录执行）")
}

func ValidateConfigMatchesAllJobs(configPath string) (Paths, error) {
	if strings.TrimSpace(configPath) == "" {
		return ResolvePaths()
	}

	absConfig, err := filepath.Abs(configPath)
	if err != nil {
		return Paths{}, fmt.Errorf("解析 config 绝对路径失败: %w", err)
	}

	paths, err := ResolvePaths()
	if err != nil {
		return Paths{}, err
	}

	equal, err := JSONFilesEqual(absConfig, paths.AllJobsPath)
	if err != nil {
		return Paths{}, err
	}
	if !equal {
		return Paths{}, fmt.Errorf("config 与 %s JSON 内容不一致: config=%s", paths.AllJobsPath, absConfig)
	}

	return paths, nil
}

func JSONFilesEqual(leftPath, rightPath string) (bool, error) {
	left, err := readJSONValue(leftPath)
	if err != nil {
		return false, err
	}
	right, err := readJSONValue(rightPath)
	if err != nil {
		return false, err
	}
	return reflect.DeepEqual(left, right), nil
}

func DockerComposeUpSpec(composeDir string, services []string) oscmdexec.Spec {
	args := []string{"compose", "up", "--build"}
	args = append(args, services...)
	return oscmdexec.Spec{
		Name: "docker",
		Args: args,
		Dir:  composeDir,
	}
}

func MultijobServices() []string {
	services := make([]string, 0, 8)
	for i := 1; i <= 8; i++ {
		services = append(services, fmt.Sprintf("multijob-%d", i))
	}
	return services
}

func buildPaths(deployClientPath string) Paths {
	cleanDeployClientDir := filepath.Clean(deployClientPath)
	return Paths{
		DeployClientDir: cleanDeployClientDir,
		AllJobsPath:     filepath.Join(cleanDeployClientDir, allJobsRelPath),
		ComposeDir:      filepath.Join(filepath.Dir(cleanDeployClientDir), benchDockerDir),
	}
}

func deployClientCandidates(wd string) []string {
	return []string{
		filepath.Join(wd, deployClientDir),
		wd,
	}
}

func readJSONValue(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取 JSON 文件失败: path=%s: %w", path, err)
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("解析 JSON 文件失败: path=%s: %w", path, err)
	}
	return v, nil
}

func isDir(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
