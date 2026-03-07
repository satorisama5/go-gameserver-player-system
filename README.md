基于 Go 的多人联机服务器

其中unityserverupgrade为服务器部分  go run cmd/gamed 和 cmd/gatewayd 以启动服务器
client部分客户端运行在unity下

项目角色：自创全栈项目
技术栈：Go, C#, Unity3D, TCP, gRPC, Redis, MongoDB, WebSocket，Rabbitmq
实现创房，查询在线，修改昵称，加入房间，房间聊天，离开房间，查询列表，私聊，移动交互等功能
1网络协议：基于 TCP 长连接 设计 [长度头+JSON] 协议包结构，处理粘包/半包问题；编写 小型WebSocket 代理层 实现兼容与自动升级。
2高并发设计：采用 读写分离 Goroutine 架构，利用 Channel 与 RWMutex 确保 OnlineMap 及房间数据在并发环境下的线程安全。
3状态同步：实现独立房间系统，以30Hz频率定时广播场景状态，实现聊天系统并配合aoi优化，并配合客户端 Lerp 插值算法 优化角色移动的平滑度，同时检测人物是否移动加速异常。
4微服务与数据层：剥离 gRPC 排行榜微服务，利用 Redis ZSet 实现高性能排序；引入 MongoDB，Rabbitmq 存储玩家位姿，并支持断线后的无缝状态恢复。
5内容安全：基于 DFA 算法与 Trie 树 构建敏感词过滤引擎，实现毫秒级聊天内容审计。环境变量引入，网关转发设置，docker部署，兼容多平台和安全性
