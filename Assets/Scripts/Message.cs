// Message.cs (已修复)
using System.Collections.Generic;

// 定义消息ID常量，必须与服务器完全一致
public static class MessageID
{
    public const int TextMessage = 0;
    public const int Command = 1001;
    public const int Heartbeat = 1002;
    public const int SceneState = 2001;
    public const int PlayerMove = 2002;
}

// 消息基类
public class Message
{
    public int id;
    // 【核心修改】将 data 的类型从 string 改为 object
    // 这可以防止在发送前进行双重序列化
    public object data;
}

// C->S 用于发送文本指令的包装类
public class CommandMessage
{
    public string cmd;
}

// C -> S: 客户端上报自己的位置
public class PlayerPosition
{
    public float x;
    public float y;
    public float z;
}

// S -> C: 服务器广播的单个玩家状态
public class PlayerState
{
    public string name;
    public PlayerPosition pos;
}

// S -> C: 服务器广播的完整场景状态
public class SceneStateBroadcast
{
    public Dictionary<string, PlayerState> players;
}