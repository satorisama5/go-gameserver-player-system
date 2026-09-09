# 基于 Go 的多人联机游戏服务器

这个项目是一个支持多人实时交互、用 Go 实现的（游戏）后端（可配合 Unity 客户端）。

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

## 仓库结构（本地 / 升级后）

| 路径 | 说明 |
|------|------|
| `cmd/gamed` | 游戏服（TCP + 同进程 gRPC） |
| `cmd/gatewayd` | gRPC 代理网关 |
| `cmd/loadtest` / `cmd/cleardb` | 压测 / 清库工具 |
| `Dockerfile.gamed` / `Dockerfile.gatewayd` | 镜像构建 |
| `docker-compose.yml` | mongo / redis / rabbitmq / 应用 |
| `.github/workflows/docker-publish.yml` | push `main` 自动构建并推送 Docker Hub |

## 本地运行（开发）

```bash
# 依赖（或 docker compose up mongo redis rabbitmq）
docker compose up -d mongo redis rabbitmq

go run ./cmd/gamed
# 可选
go run ./cmd/gatewayd
```

默认端口见 `config.yaml`（常见：TCP `8888`，gRPC `9090`）。

## Docker Hub + CI（事件驱动）

**不是**仓库轮询代码，而是 **`git push` → GitHub webhook → Actions 构建镜像 → 推到 Docker Hub**。

### 1. Docker Hub 上要做什么

1. 浏览器打开 [https://hub.docker.com](https://hub.docker.com)，用与 Docker Desktop 相同的账号登录（你本机 Desktop 已登录也可点左侧 **Docker Hub** 进网页）。
2. 右上角头像 → **Account Settings** → 看清自己的 **Username**（不一定等于邮箱前缀）。
3. **Repositories → Create repository**，建两个（Public 即可），本项目已用：
   - `engetsu/u3dgame`
   - `engetsu/u3dgatewayd`
4. **Account Settings → Security → New Access Token**（Read, Write），复制保存。

> Docker Desktop「已登录」只方便本机 `docker pull/push`；**GitHub Actions 推镜像必须再用 Access Token 配到 GitHub Secrets**，两者不是一回事。

### 2. GitHub Secrets

仓库 → **Settings → Secrets and variables → Actions**：

| Secret 名 | 值 |
|-----------|-----|
| `DOCKERHUB_USERNAME` | Hub 上的 Username |
| `DOCKERHUB_TOKEN` | 上一步 Access Token |

### 3. 推送代码触发构建

把本仓库完整代码推到 `main`（或先推功能分支再合并）后，打开 **Actions** 查看 `Build and push Docker images`。

成功后本机可验证：

```bash
docker pull engetsu/u3dgame:latest
docker pull engetsu/u3dgatewayd:latest
```

## License

自用学习 / 作品集项目。
