using System;
using System.Net.WebSockets;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using UnityEngine;
using UnityEngine.UI; // 如果你想绑定按钮

public class SimpleWSClient : MonoBehaviour
{
    private ClientWebSocket _ws;

    // 用于测试的 UI 按钮（可选）
    // public Button connectBtn;
    // public Button sendBtn;

    async void Start()
    {
        // 自动连接测试
        await ConnectToWS();
    }

    async Task ConnectToWS()
    {
        _ws = new ClientWebSocket();
        Uri serverUri = new Uri("ws://127.0.0.1:8889"); // 连接代理端口

        try
        {
            await _ws.ConnectAsync(serverUri, CancellationToken.None);
            Debug.Log("<color=green>WebSocket 连接成功！</color>");

            // 启动接收循环
            ReceiveLoop();

            // 发送一条登录指令测试一下
            // 注意：这里发送的内容必须符合服务器的 JSON 协议，但不需要加长度头！
            // 假设协议是：ID 1001 = Rename/Login
            string loginJson = "{\"id\":1001, \"data\":{\"cmd\":\"rename|WS_Player\"}}";
            await SendString(loginJson);
        }
        catch (Exception e)
        {
            Debug.LogError("WS 连接失败: " + e.Message);
        }
    }

    async Task SendString(string msg)
    {
        if (_ws.State == WebSocketState.Open)
        {
            byte[] buffer = Encoding.UTF8.GetBytes(msg);
            // WebSocket 发送不需要加 2字节头，代理会帮我们加
            await _ws.SendAsync(new ArraySegment<byte>(buffer), WebSocketMessageType.Text, true, CancellationToken.None);
            Debug.Log($"[WS发送] {msg}");
        }
    }

    async void ReceiveLoop()
    {
        byte[] buffer = new byte[1024];
        while (_ws.State == WebSocketState.Open)
        {
            var result = await _ws.ReceiveAsync(new ArraySegment<byte>(buffer), CancellationToken.None);
            if (result.MessageType == WebSocketMessageType.Close)
            {
                await _ws.CloseAsync(WebSocketCloseStatus.NormalClosure, string.Empty, CancellationToken.None);
            }
            else
            {
                // 收到消息（代理已经帮我们去掉了 2字节头）
                string msg = Encoding.UTF8.GetString(buffer, 0, result.Count);
                Debug.Log($"[WS接收] {msg}");
            }
        }
    }

    private async void OnDestroy()
    {
        if (_ws != null && _ws.State == WebSocketState.Open)
            await _ws.CloseAsync(WebSocketCloseStatus.NormalClosure, "Quit", CancellationToken.None);
    }
}