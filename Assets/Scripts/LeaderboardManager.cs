// LeaderboardManager.cs (最终统一版)

// 【修正1】删除所有不必要的 using，只保留 Grpc.Core
using UnityEngine;
using Grpc.Core;
using Leaderboard;
using System.Threading.Tasks;

public class LeaderboardManager : MonoBehaviour
{
    public static LeaderboardManager Instance;

    // 【修正2】变量类型必须是 Grpc.Core 中的 'Channel'
    private Channel _channel;
    private LeaderboardService.LeaderboardServiceClient _client;

    void Awake()
    {
        if (Instance == null)
        {
            Instance = this;
            DontDestroyOnLoad(gameObject);
        }
        else
        {
            Destroy(gameObject);
        }
    }

    void Start()
    {
        System.Environment.SetEnvironmentVariable("http_proxy", null);
        System.Environment.SetEnvironmentVariable("https_proxy", null);
        System.Environment.SetEnvironmentVariable("no_proxy", "127.0.0.1,localhost");
        System.Environment.SetEnvironmentVariable("grpc_proxy", null);

        Debug.Log("环境变量已清理，准备连接 gRPC...");
        _channel = new Channel("127.0.0.1:8080", ChannelCredentials.Insecure);

        _client = new LeaderboardService.LeaderboardServiceClient(_channel);
        Debug.Log("LeaderboardManager 已初始化 (使用 Grpc.Core)");
    }

    // 异步提交分数 (这部分逻辑完全不变)
    public async Task<bool> SubmitScoreAsync(string playerName, int score)
    {
        if (_client == null) return false;
        try
        {
            var request = new SubmitScoreRequest { PlayerName = playerName, Score = score };
            var response = await _client.SubmitScoreAsync(request);
            Debug.Log("分数提交成功！");
            return response.Success;
        }
        catch (RpcException e)
        {
            Debug.LogError("RPC Error: " + e.Status);
            return false;
        }
    }

    // 异步获取排行榜 (这部分逻辑完全不变)
    public async Task<GetTopPlayersResponse> GetLeaderboardAsync(int limit)
    {
        if (_client == null) return null;
        try
        {
            var request = new GetTopPlayersRequest { Limit = limit };
            var response = await _client.GetTopPlayersAsync(request);
            return response;
        }
        catch (RpcException e)
        {
            Debug.LogError("RPC Error: " + e.Status);
            return null;
        }
    }

    void OnDestroy()
    {
        if (_channel != null)
        {
            _channel.ShutdownAsync().Wait();
        }
    }
}