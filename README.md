# ydyl-deploy-client

`ydyl-deploy-client` 是一个基于 Go + Cobra 的批量部署 CLI，主要用于：

- 在 AWS 上批量创建 EC2
- 等待 SSH 就绪并执行远程部署命令
- 持久化部署结果和脚本状态
- 基于已部署链节点生成跨链压测 jobs
- 配合 `ydyl-bench-docker` 启动批量压测与 TPS 监控

它已经不是单纯的“起机器脚本”，而是当前仓库跨链压测工作流的入口之一。

## 典型工作流

最常见的顺序是：

1. `deploy`
2. `gen-cross-tx-config`
3. 进入 `../ydyl-bench-docker` 执行 `docker-compose up --build`

对应关系：

- `deploy` 负责把链节点和服务拉起来
- `gen-cross-tx-config` 负责读取 `servers.json` 和各节点上的 `ydyl-console-service`，生成 `zk-claim-service/scripts/7s_multijob.js` 所需 jobs
- `ydyl-bench-docker` 会基于这些 jobs 启动 8 个发送容器和 1 个 TPS 监控容器

## 编译与运行

```bash
cd ydyl-deploy-client

# 编译
go build -o ydyl-deploy-client .

# 直接运行
go run . deploy -f config.deploy.yaml
```

前置要求：

- 已配置 AWS 访问凭证
- 本机有与 `keyName` 匹配的 SSH 私钥
- 目标实例镜像中已具备仓库运行所需基础环境，或会在远程命令里自行准备

## 主命令

### `deploy`

作用：

- 根据 `config.deploy.yaml` 批量创建或复用 EC2
- 等待实例进入 `running`
- 轮询 SSH 就绪
- 远程执行链部署命令
- 持续同步日志与脚本状态

示例：

```bash
go run . deploy -f config.deploy.yaml
```

常见输出：

- `output/servers.json`
- `output/script_status.json`
- `output/servers_create.json`
- `logs/`

说明：

- `servers.json` 是后续 `gen-cross-tx-config` 的直接输入
- `script_status.json` 可用于 `deploy-restore` / `sync`

### `gen-cross-tx-config`

这是顶层文档里提到的 `gen-tx-config` 对应的真实子命令名。

作用：

- 读取 `servers.json`
- 访问各链节点上的 `ydyl-console-service`
- 收集 L2 RPC、桥合约、Counter 合约等信息
- 生成 `zk-claim-service/scripts/7s_multijob.js` 使用的 jobs

示例：

```bash
go run . gen-cross-tx-config \
  --servers ./output/servers.json \
  --config ./config.deploy.yaml
```

默认输出：

- `output/jobs/all.json`
- `output/jobs/1.json`
- `output/jobs/2.json`
- ...
- `output/jobs/8.json`

常用参数：

- `--servers`
  - `servers.json` 路径，必填
- `--config`
  - deploy 配置文件路径，默认 `./config.deploy.yaml`
- `--out`
  - 输出根目录；默认使用 `servers.json` 所在目录
- `--part-number`
  - jobs 拆分份数，默认 `8`
- `--tx-amount-per-wallet`
  - 每个 wallet 发送交易数量，默认 `1000`
- `--wallet-amount`
  - 每个 job 使用的 wallet 数量，默认 `10`
- `--block-range`
  - TPS 查询区块范围，默认 `100000`

说明：

- 默认生成出来的 `output/jobs` 正好是 `../ydyl-bench-docker/docker-compose.yml` 的挂载目录
- 如果你自定义了 `--out`，需要同步调整 `ydyl-bench-docker` 的 bind mount 路径

## 压测联动

当 `deploy` 和 `gen-cross-tx-config` 都完成后，通常直接启动 Docker 压测：

```bash
cd ../ydyl-bench-docker
docker-compose up --build
```

这会启动：

- `multijob-1` 到 `multijob-8`
- `tps`

它们分别对应：

- 8 个 `zk-claim-service/scripts/7s_multijob.js` 发送进程
- 1 个 `zk-claim-service/scripts/h_TPSjob.js` 监控进程

## 可选辅助命令

除了主流程，当前 CLI 还包含一些恢复 / 调试命令：

- `sync`
  - 基于已有 `servers.json` / `script_status.json` 重新同步日志和状态
- `deploy-restore`
  - 基于 `script_status.json` 仅恢复失败或未完成的远程部署任务
- `shutdown`
  - 按 `servers.json` 对远端机器执行关机
- `bench-cross-tx`
  - 不走 Docker，直接本地执行 `zk-claim-service/scripts/7s_multijob.js`
- `tps`
  - 不走 Docker，直接本地执行 `zk-claim-service/scripts/h_TPSjob.js`

## 配置文件

建议从 [config.deploy.example.yaml](/Users/dayong/myspace/mywork/ydyl-deployment-suite/ydyl-deploy-client/config.deploy.example.yaml:1) 开始。

关键配置包括：

- AWS / EC2 基本参数
  - `region`
  - `securityGroupId`
  - `diskSizeGiB`
  - `keyName`
- SSH 与输出
  - `sshUser`
  - `sshKeyDir`
  - `logDir`
  - `outputDir`
- L1 通用配置
  - `l1ChainId`
  - `l1RpcUrl`
  - `l1RpcUrlWs`
  - `l1BridgeHubContract`
  - `l1RegisterBridgePrivateKey`
- 服务列表
  - `services[].type`
  - `services[].count`
  - `services[].ami`
  - `services[].instanceType`
  - `services[].tagPrefix`
  - `services[].remoteCmd`

当前支持的服务类型主要包括：

- `op`
- `cdk`
- `xjst`
- `generic`

## 输出文件说明

部署阶段：

- `output/servers_create.json`
  - 创建实例后拿到的原始候选服务器快照
- `output/servers.json`
  - 当前参与部署 / 后续操作的服务器列表
- `output/script_status.json`
  - 远程脚本执行状态，供恢复和同步使用
- `output/ssh_scripts.json`
  - 远程执行脚本记录

压测配置阶段：

- `output/jobs/all.json`
  - 全量跨链 jobs，适合 `h_TPSjob.js`
- `output/jobs/1.json ~ 8.json`
  - 拆分后的 job 文件，适合并行发送

## 建议

- `deploy` 完成后先确认 `ydyl-console-service` 可从各节点访问，否则 `gen-cross-tx-config` 会失败
- `servers.json` 和 `output/jobs/` 建议保留一份归档，便于复现实验
- 如果需要重跑压测但不重建 EC2，优先复用已有 `servers.json` 和 jobs 输出
