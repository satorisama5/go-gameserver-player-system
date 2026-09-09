# 第四步：ArgoCD GitOps（在你现有 CI + K8s 上继续）

你已经有：push → Actions 构建 → Hub；kubectl 部署。  
这一步去掉「手动改 yaml / 手动 apply」，变成 **CI 改仓库里的镜像 tag → ArgoCD 自动同步集群**。

> 本机是 **Docker Desktop K8s**，不是 minikube；装 ArgoCD 同样用 `kubectl`。  
> 配置先放在**本仓库** `deploy/k8s`（同仓 GitOps，好练）。面试可提：大厂常再拆独立「配置仓」。

## 你要自己敲的命令

### A. 安装 ArgoCD

```powershell
kubectl create namespace argocd
kubectl apply -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl get pods -n argocd -w
```

全部 Running 后 Ctrl+C。

### B. 打开界面（用 8443，少和别的 8080 抢）

```powershell
kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}" 
```

把输出的 base64 解码（PowerShell）：

```powershell
$b64 = kubectl -n argocd get secret argocd-initial-admin-secret -o jsonpath="{.data.password}"
[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String($b64))
```

记住密码。然后：

```powershell
kubectl port-forward svc/argocd-server -n argocd 8443:443
```

浏览器打开：`https://localhost:8443`（证书警告选继续）  
用户名：`admin`，密码：上一步。

### C. 注册 Application（二选一）

**方式 1（推荐，已写好清单）：**

```powershell
kubectl apply -f deploy/argocd/application.yaml
```

**方式 2：** 网页 New App，按 `application.yaml` 里字段填（Sync Policy 选 Automatic）。

几秒后应显示 Synced / Healthy；集群里仍是你的 mongo/redis/u3dgame 等。

### D. 验证「改 Git → 自动变集群」

1. 把本仓库含 CI 改镜像逻辑的代码 **push 到 upgrade-2026**（需代理时记得 7892）。  
2. Actions 构建成功后，会自动 commit：改 `deploy/k8s/02-u3dgame.yaml` / `03-gatewayd.yaml` 的 image tag（带 `[skip ci]`，避免死循环）。  
3. 看 ArgoCD：短暂 OutOfSync → 再 Synced；或：

```powershell
kubectl get pods -w
```

应出现滚动更新。

## 链路（面试口述）

```text
改代码 → git push
  → Actions：build/push Hub + 回写 deploy/k8s 镜像版本
  → ArgoCD 发现 Git 变了 → 自动 sync
  → K8s 滚动更新 Pod
```

## 注意

- 第一次装 ArgoCD 要能访问 `raw.githubusercontent.com`（可能要代理）。  
- 仓库若仍是 Public，ArgoCD 拉配置一般不用额外 token。  
- 独立配置仓是加分项，不是这一步必做。
