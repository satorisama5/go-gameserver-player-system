using System;
using UnityEngine;
using System.Net.Sockets;
using System.Text;
using Newtonsoft.Json;
using System.Collections;

public class ClientNet : MonoBehaviour
{
    private Action<bool> m_connectCb;
    private Action<string> m_recvMsgCb;
    private ConnectState m_connectState = ConnectState.None;
    private IAsyncResult m_connectAsync;
    private byte[] m_recvBuf;
    private int m_readOffset;
    private int m_recvOffset;
    private Socket m_socket;
    private NetworkStream m_networkStream;
    private static ClientNet s_instance;

    // 心跳间隔
    private const float HEARTBEAT_INTERVAL = 5.0f;
    private float m_heartbeatTimer = 0f;

    // 断线回调事件
    public Action OnDisconnected;

    // 标记是否是用户主动退出 (防止主动退出时也触发自动重连)
    public bool IsUserKick { get; set; } = false;

    // 【新增】线程安全标志位：标记是否需要触发断线事件
    // 因为 CloseSocket 可能在子线程被调用，而事件必须在主线程触发
    private bool m_needFireDisconnectEvent = false;

    private void Awake()
    {
        // 这里只初始化缓冲区，Socket的初始化移到Connect中，方便重连
        m_readOffset = 0;
        m_recvOffset = 0;
        m_recvBuf = new byte[0x4000]; // 16KB
    }

    private void Update()
    {
        // 1. 检查连接结果
        if (m_connectState == ConnectState.Ing && m_connectAsync != null && m_connectAsync.IsCompleted)
        {
            if (m_socket != null && m_socket.Connected)
            {
                // 连接成功，回调已经在 callback 中处理
            }
            else
            {
                m_connectState = ConnectState.None;
                if (null != m_connectCb)
                {
                    m_connectCb(false);
                    m_connectCb = null;
                }
            }
        }

        // 2. 正常连接状态下的逻辑
        if (m_connectState == ConnectState.Ok)
        {
            TryRecvMsg();

            // 心跳逻辑
            m_heartbeatTimer += Time.deltaTime;
            if (m_heartbeatTimer >= HEARTBEAT_INTERVAL)
            {
                m_heartbeatTimer = 0f;
                SendHeartbeat();
            }
        }

        // 3. 【核心修复】在主线程检测并触发断线事件
        if (m_needFireDisconnectEvent)
        {
            m_needFireDisconnectEvent = false;
            Debug.Log("主线程触发断线事件...");
            if (OnDisconnected != null)
            {
                OnDisconnected();
            }
        }
    }

    public void Connect(string host, int port, Action<bool> cb)
    {
        // 【核心修复】断线重连时，m_socket 是 null，必须重新 new 一个
        if (m_socket == null)
        {
            m_socket = new Socket(AddressFamily.InterNetwork, SocketType.Stream, ProtocolType.Tcp);
        }

        // 重置状态
        IsUserKick = false;
        m_needFireDisconnectEvent = false;

        m_connectCb = cb;
        m_connectState = ConnectState.Ing;

        m_socket.BeginConnect(host, port, (IAsyncResult result) =>
        {
            try
            {
                var socket = result.AsyncState as Socket;
                if (socket == null) return;

                socket.EndConnect(result);
                m_connectState = ConnectState.Ok;
                m_networkStream = new NetworkStream(m_socket);
                Debug.Log("Connect Ok");

                // 注意：这里是在子线程
                if (null != m_connectCb) m_connectCb(true);
            }
            catch (Exception e)
            {
                Debug.LogError("Connect failed: " + e.ToString());
                if (null != m_connectCb) m_connectCb(false);
                m_connectState = ConnectState.None;
            }
        }, m_socket);
    }

    private void TryRecvMsg()
    {
        if (m_socket == null || !m_socket.Connected) return;
        if (!m_socket.Poll(0, SelectMode.SelectRead)) return;

        m_socket.BeginReceive(m_recvBuf, m_recvOffset, m_recvBuf.Length - m_recvOffset, SocketFlags.None, (result) =>
        {
            try
            {
                if (m_socket == null) return; // 防止异步期间socket被置空

                var len = m_socket.EndReceive(result);
                if (len <= 0)
                {
                    CloseSocket(); // 收到0字节，代表断开
                    return;
                }

                m_recvOffset += len;
                m_readOffset = 0;

                while (m_recvOffset - m_readOffset >= 2)
                {
                    int msgLen = (m_recvBuf[m_readOffset] << 8) | m_recvBuf[m_readOffset + 1];

                    if (m_recvOffset >= (m_readOffset + 2 + msgLen))
                    {
                        string msg = Encoding.UTF8.GetString(m_recvBuf, m_readOffset + 2, msgLen);

                        // 回调接收到的消息 (注意：这里是在子线程)
                        // 如果 UI 操作需要主线程，建议在 GameManager 使用 Queue 转发，或者确保 recvMsgCb 只是入队
                        if (null != m_recvMsgCb)
                            m_recvMsgCb(msg);

                        m_readOffset += 2 + msgLen;
                    }
                    else
                    {
                        break;
                    }
                }

                if (m_readOffset > 0)
                {
                    Buffer.BlockCopy(m_recvBuf, m_readOffset, m_recvBuf, 0, m_recvOffset - m_readOffset);
                    m_recvOffset -= m_readOffset;
                }
            }
            catch (Exception e)
            {
                Debug.LogError("Recv msg failed: " + e.ToString());
                CloseSocket();
            }
        }, null);
    }

    public void SendMessage(int msgId, object data)
    {
        if (!IsConnected()) return;

        Message msg = new Message { id = msgId, data = data };
        string finalJson = JsonConvert.SerializeObject(msg);

        byte[] jsonBytes = Encoding.UTF8.GetBytes(finalJson);
        byte[] package = new byte[jsonBytes.Length + 2];

        package[0] = (byte)(jsonBytes.Length >> 8);
        package[1] = (byte)(jsonBytes.Length & 0xFF);
        Buffer.BlockCopy(jsonBytes, 0, package, 2, jsonBytes.Length);

        try
        {
            if (m_networkStream != null && m_networkStream.CanWrite)
            {
                m_networkStream.Write(package, 0, package.Length);
            }
        }
        catch (Exception e)
        {
            Debug.LogError("Send Message Failed: " + e.Message);
            CloseSocket();
        }
    }

    private void SendHeartbeat()
    {
        // 发送心跳包，内容可以是任意字符串，服务器只看ID
        SendMessage(MessageID.Heartbeat, "ping");
    }

    public void CloseSocket()
    {
        // 如果已经是断开状态，就别重复处理了
        if (m_connectState == ConnectState.None) return;

        Debug.Log("CloseSocket called.");

        if (m_socket != null)
        {
            try
            {
                if (m_socket.Connected) m_socket.Shutdown(SocketShutdown.Both);
                m_socket.Close();
            }
            catch (Exception e) { Debug.LogWarning("CloseSocket error: " + e.Message); }
        }

        m_socket = null;
        m_networkStream = null;
        m_connectState = ConnectState.None;

        // 【核心修改】不要直接调用 Action，而是设置标志位
        // 这样 Update 循环会在主线程检测到，并安全地触发事件
        if (!IsUserKick)
        {
            m_needFireDisconnectEvent = true;
        }

        IsUserKick = false; // 重置踢出状态
    }

    public bool IsConnected()
    {
        return m_socket != null && m_socket.Connected && m_connectState == ConnectState.Ok;
    }

    public void RegistRecvMsgCb(Action<string> cb)
    {
        m_recvMsgCb = cb;
    }

    private enum ConnectState { None, Ing, Ok, }

    public static ClientNet instance
    {
        get
        {
            if (null == s_instance)
            {
                var go = new GameObject("ClientNet");
                DontDestroyOnLoad(go);
                s_instance = go.AddComponent<ClientNet>();
            }
            return s_instance;
        }
    }
}