# 本地 K8s 部署（Docker Desktop Kubernetes）

> 你本机是 **Docker Desktop 自带 K8s**（`kubectl get nodes` 显示 `docker-desktop`），**不需要 minikube**。  
> 教程里写 minikube 只是另一种本地集群；有 Desktop K8s 直接用 `kubectl` 即可。

## 全链路在讲什么

```text
改代码 → git push → Actions CI 构建 → 推 Docker Hub
      → kubectl apply → K8s 从 Hub 拉镜像 → Pod 运行
```

这是 **CI（构建发布制品）+ 本机 CD 演示（部署到 K8s）**。自动改 Deployment 镜像 tag 可后续再加。

## 一次性部署

确保 Docker Desktop → Settings → Kubernetes → Enable 已开，且：

```powershell
kubectl get nodes
# 应看到 docker-desktop Ready
```

```powershell
cd "e:\unity project\unityserverupgrade"

# 1) 依赖：Mongo / Redis / RabbitMQ
kubectl apply -f deploy/k8s/01-deps.yaml

# 2) 游戏服（从 Hub 拉 engetsu/u3dgame）
kubectl apply -f deploy/k8s/02-u3dgame.yaml

# 3) 可选网关
kubectl apply -f deploy/k8s/03-gatewayd.yaml

kubectl get pods -w
```

Pod 变 `Running` 后：

- 游戏 TCP：`localhost:30888`（NodePort）
- gRPC：`localhost:30909`
- gateway：`localhost:30808`

## 可追溯部署（推荐演示）

CI 成功后 Hub 上会有 commit sha tag（不只 `latest`）。改 `02-u3dgame.yaml`：

```yaml
image: engetsu/u3dgame:9563d35   # 换成 Actions/Hub 上真实 sha
```

再：

```powershell
kubectl apply -f deploy/k8s/02-u3dgame.yaml
kubectl rollout status deployment/u3dgame
kubectl get pods -o wide
```

## 体验「更新」

1. 改一行代码 → commit → `git push origin HEAD:upgrade-2026`（代理 7892）
2. Actions 绿 → Hub 出现新 tag
3. 改 yaml 里 `image:` 为新 tag → `kubectl apply` → `rollout status`

## 注意

- 演示用 **replicas: 1**：房间状态在进程内存，多副本要粘性/分片，面试可口述。
- 端口是 **8888/9090**，不是示例里的 5000。
- 镜像在 **Docker Hub**，不是 GitHub Packages。
