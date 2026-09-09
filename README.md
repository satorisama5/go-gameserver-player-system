# 基于 Go 的多人联机游戏服务器

这个项目是一个支持多人实时交互、用 Go 实现的（游戏）后端（可配合 Unity 客户端）。

> **仓库说明（重要）：** 当前 GitHub 仓库**只上传了服务端（Go）部分**，用于作品集展示与 Docker/CI 演示。  
> **Unity 客户端工程未包含在本仓库中**（客户端仍在本地其它目录维护）。下文「可配合 Unity 客户端」指架构上可对接，不代表本仓库内有客户端源码。

## 技术栈

Go · TCP · gRPC · Redis · MongoDB · RabbitMQ · Docker · GitHub Actions

## 项目概括

**服务拆分双入口：**

1. **TCP 长连接（gamed）：** 承载实时（游戏）通信，基于 net.Conn 解析自定义帧并分发处理；连接管理，在线基础指令，用户系统，登录鉴权/令牌重连/顶号、私聊·好友·世界（群组）广播、房间（群组）管理、位置与场景同步、对战与 Buff（状态时间轮）、任务进度推进、本服/跨服 PVP（ELO）、房间（群组）协同笔记等。

2. **gRPC 对内服务：** 提供钱包查余额、幂等扣费与击杀（任务）发奖（outbox），以及排行榜增量加分和 TopN 查询。

**存储与中间件：**  
MongoDB 持久化账户存档、聊天、房间（群组）、背包、钱包账本、笔记快照、任务状态表、事件日志等；Redis 做缓存（含防穿透）、短窗幂等、限流、分布式锁、房间快照、跨服匹配队列，以及玩家→实例路由的简单分布式；RabbitMQ 异步落库聊天与笔记快照。分布式聊天可跨机转发；跨服匹配的客机软迁移至主办实例再权威结算。

**可观测与排障：**  
结构化事件日志入库并建立数据库索引，支持标识 id 排查与时段统计；关键写路径有幂等回执与统一埋点。

**性能与工程化：**  
场景分格 AOI + 增量广播；连接心跳超时下线，校验发包频率与移动合法性；敏感词过滤；Docker 多环境部署；压测验证连接、吞吐与失败率指标。

## 仓库结构

| 路径 | 说明 |
|------|------|
| `cmd/gamed` | 游戏服（TCP + 同进程 gRPC） |
| `cmd/gatewayd` | gRPC 代理网关 |
| `cmd/loadtest` / `cmd/cleardb` | 压测 / 清库工具 |
| `Dockerfile.gamed` / `Dockerfile.gatewayd` | 镜像构建 |
| `docker-compose.yml` | mongo / redis / rabbitmq / 应用 |
| `.github/workflows/docker-publish.yml` | push 后自动构建并推送 Docker Hub |

## 本地运行（开发）

```bash
docker compose up -d mongo redis rabbitmq
go run ./cmd/gamed
# 可选
go run ./cmd/gatewayd
```

默认端口见 `config.yaml`（常见：TCP `8888`，gRPC `9090`）。

## Docker Hub + CI（事件驱动）

**不是**仓库轮询代码，而是 **`git push` → GitHub webhook → Actions 构建镜像 → 推到 Docker Hub**。

### 镜像仓库（看新镜像来这里）

构建成功后，镜像在 **Docker Hub**，不在 GitHub Packages：

- [engetsu/u3dgame](https://hub.docker.com/r/engetsu/u3dgame/tags)（游戏服）
- [engetsu/u3dgatewayd](https://hub.docker.com/r/engetsu/u3dgatewayd/tags)（gRPC 网关）

Tags 一般有 `latest` 和 commit sha。GitHub 右侧 **Packages** 为空是正常的。

本机拉取：

```bash
docker pull engetsu/u3dgame:latest
docker pull engetsu/u3dgatewayd:latest
```

### 如何触发 CI

1. 向分支 **`main` 或 `upgrade-2026`** 执行 `git push`（本仓库 workflow 已监听这两支）。  
2. 打开 GitHub 仓库顶部 **Actions** → 点开 **Build and push Docker images**，看是否绿勾。  
3. 可选：Actions 页选该 workflow → **Run workflow**（`workflow_dispatch`；需默认分支上也有此文件时更稳）。

仓库 Secrets（已配置则跳过）：`DOCKERHUB_USERNAME` = `engetsu`，`DOCKERHUB_TOKEN` = Hub Access Token。

## License

自用学习 / 作品集项目。服务端部分开源展示；客户端代码未上传。
