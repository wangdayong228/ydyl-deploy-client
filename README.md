## 简介

`ydyl-deploy-client` 是一个使用 Go 和 Cobra 编写的命令行工具，用于**批量在 AWS 上启动 EC2 实例，并在实例上按策略执行部署脚本**。  
实现上参考了本仓库中 `op-work/scripts/aws-batch.sh` / `cdk-work/scripts/aws-batch.sh` 的流程，但**完全使用 Go 版实现，不再依赖 bash 脚本本身**。

主要特性：

- **多 service 支持**：一次部署中可以同时配置多种服务类型（如 `op`、`cdk`、`generic`、`xjst`），每种服务的数量与远程命令独立配置。
- **自动化流程**：对每个 service 执行「创建 EC2 → 等待 running → 获取公网 IP → 等待 SSH 就绪 → 批量执行命令 → 收集日志」的完整流程。
- **YAML 配置驱动**：所有参数通过一个 YAML 配置文件管理，命令行只需传入 `--config`。

## 安装与编译

在仓库根目录：

```bash
cd ydyl-deployment-suite/ydyl-deploy-client

# 编译二进制
go build -o ydyl-deploy-client .

# 或直接运行
go run . deploy -f deploy.yaml
```

要求：

- 已正确配置 `AWS CLI` / 环境变量（`AWS_REGION` / `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` 等）。
- 本机存在对应的 SSH 私钥文件（例如 `~/.ssh/dayong-op-stack.pem`），并与配置中的 `keyName` 匹配。

## 命令说明

当前只实现一个核心子命令：

- `deploy`：根据 YAML 配置文件批量创建 EC2 实例并执行远程命令。

用法：

```bash
ydyl-deploy-client deploy -f deploy.yaml [--log-dir logs/override]
```

参数：

- `-f, --config`（必选）：部署配置文件路径（YAML 格式）。
- `--log-dir`（可选）：覆盖配置文件中的 `logDir`，用于指定本地日志输出目录。

## 配置文件格式（YAML）

示例 `deploy.yaml`：

```yaml
region: ap-southeast-1            # AWS 区域
ami: ami-0d5d4434d0110b7e1        # AMI ID
instanceType: c6a.xlarge          # 实例类型
keyName: dayong-op-stack          # EC2 Key Pair 名称（对应 ~/.ssh/dayong-op-stack.pem）
securityGroupId: sg-02452e70d9fe7e235
tagPrefix: dy-op                  # 实例 Name 标签前缀

runDuration: 50m                  # 远程机器运行时长（time.Duration 格式），会转成 sudo -n shutdown -h +N
sshUser: ubuntu                   # SSH 用户名
sshKeyDir: ""                     # 为空则使用 ~/.ssh
logDir: logs                      # 本地日志目录（可被 --log-dir 覆盖）

services:
  # 1) OP 链部署：不显式指定 remoteCmd，则使用内置策略：
  #    每台机器生成一个 PRIVATE_KEY（0x + 64 位 hex）和 L2_CHAIN_ID=10000+i
  #    命令：cd /home/ubuntu/op-work/scripts/deploy-op-stack && PRIVATE_KEY=... L2_CHAIN_ID=... ./deploy-with-env.sh
  - type: op
    count: 3
    remoteCmd: ""

  # 2) CDK 部署：这里示例为显式给出远程命令
  - type: cdk
    count: 2
    remoteCmd: >
      cd /home/ubuntu/cdk-work/scripts &&
      L2_CHAIN_ID=10001 L1_CHAIN_ID=71 L1_RPC_URL=https://cfx-testnet-cdk-rpc-proxy.yidaiyilu0.site
      ./deploy.sh cdk-gen

  # 3) 通用服务：必须显式指定 remoteCmd
  - type: generic
    count: 1
    remoteCmd: "cd /home/ubuntu/some-service && ./start.sh"
```

字段说明：

- **region / ami / instanceType / keyName / securityGroupId / tagPrefix**：与 `aws ec2 run-instances` 参数一一对应。
- **runDuration**：计划关机时间，`time.Duration` 格式（如 `50m`，`1h`）。
- **sshUser / sshKeyDir**：SSH 登录用户与私钥所在目录。
- **logDir**：本地日志目录，每台实例一个日志文件，文件名中包含 IP 与 Name 标签。
- **services**：
  - `type`：服务类型，枚举值之一：`generic` / `op` / `cdk` / `xjst`。
  - `count`：该服务需要启动的实例数量。
  - `remoteCmd`：远程执行命令；为空时由对应 `type` 的策略自动生成（目前只对 `op` 做了默认策略）。

## 行为细节

对每个 `services` 中的条目，工具会依次执行：

1. 调用 AWS SDK：创建 `count` 台 EC2 实例，打上初始 `Name` 标签（前缀为 `tag_prefix-type-i`）。
2. 等待所有实例进入 `running` 状态。
3. 查询实例公网 IP 列表。
4. 循环使用本机 `ssh` 命令探测连通性，直到 SSH 就绪。
5. 为每台机器构造远程命令（根据 `type` 与 `remoteCmd`），再加上自动关机前缀：`sudo -n shutdown -h +runDuration(分钟) && <remoteCmd>`。
6. 执行远程命令，标准输出与错误重定向到本地日志文件。

任一实例执行失败会在本地日志中体现，并通过返回错误退出。

## 常见用法示例

```bash
# 使用示例配置文件 deploy.yaml 进行部署
./ydyl-deploy-client deploy -f deploy.yaml

# 同一配置文件，但把日志写到自定义目录
./ydyl-deploy-client deploy -f deploy.yaml --log-dir logs/op-2025-01-01
```

## 注意事项

- **请务必确认 AWS 账户费用与配额**：本工具会一次性启动多台按小时计费的 EC2 实例。
- 默认会设置实例在 `runDuration` 对应的分钟数后自动关机，但并不会释放 EBS 产生的所有费用，请自行根据实际情况清理资源。
- 建议先在测试账号 / 测试区域用较小的 `count` 验证流程，再在生产环境大规模使用。


