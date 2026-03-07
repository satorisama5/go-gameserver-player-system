## 基于 Go 的多人联机服务器

**项目角色**：自创全栈项目  
**技术栈**：Go, C#, Unity3D, TCP, gRPC, Redis, MongoDB, WebSocket, RabbitMQ

> `unityserverupgrade` 为服务器部分，通过 `go run cmd/gamed` 和 `go run cmd/gatewayd` 启动

### 核心功能
创房、查询在线、修改昵称、加入房间、房间聊天、离开房间、查询列表、私聊、移动交互

### 技术亮点

1. **网络协议**：基于 TCP 长连接设计 `[长度头+JSON]` 协议包结构，处理粘包/半包问题；编写小型 WebSocket 代理层实现兼容与自动升级。

2. **高并发设计**：采用读写分离 Goroutine 架构，利用 Channel 与 RWMutex 确保 OnlineMap 及房间数据在并发环境下的线程安全。

3. **状态同步**：实现独立房间系统，以 30Hz 频率定时广播场景状态，配合 AOI 优化聊天系统，并结合客户端 Lerp 插值算法优化角色移动平滑度，同时检测移动加速异常。

4. **微服务与数据层**：剥离 gRPC 排行榜微服务，利用 Redis ZSet 实现高性能排序；引入 MongoDB、RabbitMQ 存储玩家位姿，支持断线后的无缝状态恢复。

5. **内容安全**：基于 DFA 算法与 Trie 树构建敏感词过滤引擎，实现毫秒级聊天内容审计；环境变量注入、网关转发、Docker 部署，兼容多平台。
