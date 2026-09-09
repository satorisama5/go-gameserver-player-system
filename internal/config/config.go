// internal/config.go
package config

import (
	"fmt"
	"github.com/spf13/viper"
	"strings"

	protocol "unityserverupgrade/internal/protocol"
)

// Config 对应 config.yaml 的顶级结构
var Conf Config

type Config struct {
	Server             ServerConfig             `mapstructure:"server"`
	Database           DatabaseConfig           `mapstructure:"database"`
	Cache              CacheConfig              `mapstructure:"cache"`
	Log                LogConfig                `mapstructure:"log"`
	AOI                AOIConfig                `mapstructure:"aoi"`
	MQ                 MQConfig                 `mapstructure:"mq"`
	EventLogAnalytics  EventLogAnalyticsConfig  `mapstructure:"event_log_analytics"`
	Auth               AuthConfig               `mapstructure:"auth"`
}

// AuthConfig 账号密码 + JWT 登录。
type AuthConfig struct {
	JWTSecret     string `mapstructure:"jwt_secret"`
	TokenTTLHours int    `mapstructure:"token_ttl_hours"`
}

// EventLogAnalyticsConfig event_logs 聚合、告警日志与只读 HTTP（默认仅本机）。
type EventLogAnalyticsConfig struct {
	Enabled                 bool   `mapstructure:"enabled"`
	ListenAddr              string `mapstructure:"listen_addr"`
	RollupIntervalSeconds   int    `mapstructure:"rollup_interval_seconds"`
	AlertIntervalSeconds    int    `mapstructure:"alert_interval_seconds"`
}

type ServerConfig struct {
	TcpPort             int            `mapstructure:"tcp_port"`
	GrpcPort            int            `mapstructure:"grpc_port"`
	WsProxyPort         int            `mapstructure:"ws_proxy_port"`
	InstanceID          string         `mapstructure:"instance_id"`
	RouteTTLSeconds     int            `mapstructure:"route_ttl_seconds"`
	ForwardListenAddr   string         `mapstructure:"forward_listen_addr"`
	ForwardPublicAddr   string         `mapstructure:"forward_public_addr"`
	TcpPublicAddr       string         `mapstructure:"tcp_public_addr"` // 客户端应连接的 host:port；空则 127.0.0.1:{tcp_port}
	MigrateLoadThreshold float64       `mapstructure:"migrate_load_threshold"` // 在线占比超过此值拒绝新登录并提示换服，默认 0.8
	MaxConnections      int            `mapstructure:"max_connections"`
	MaxPacketsPerSecond int                     `mapstructure:"max_packets_per_second"`
	MaxPlayerSpeed      float64                 `mapstructure:"max_player_speed"`
	SpawnPoint          protocol.PlayerPosition `mapstructure:"spawn_point"`
	BusinessWorkers     int                     `mapstructure:"business_workers"`
	BusinessQueueSize   int                     `mapstructure:"business_queue_size"`
	InboundQueueSize    int                     `mapstructure:"inbound_queue_size"`
}
type MQConfig struct {
	Url              string `mapstructure:"url"`
	RoomQueueName    string `mapstructure:"room_queue_name"`
	PrivateQueueName string `mapstructure:"private_queue_name"`
	NoteQueueName    string `mapstructure:"note_queue_name"` // 可选；空则默认 room_note_snapshots
}
type AOIConfig struct {
	GridSize int `mapstructure:"grid_size"`
}
type DatabaseConfig struct {
	MongoURI string `mapstructure:"mongo_uri"`
}

type CacheConfig struct {
	RedisAddr string `mapstructure:"redis_addr"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

// InitConfig 初始化函数，在 main.go 中调用
func InitConfig() {
	viper.SetConfigName("config") // 配置文件名 (不带后缀)
	viper.SetConfigType("yaml")   // 配置文件类型
	viper.AddConfigPath(".")      // 配置文件路径 (当前目录)
	// --- 【新增】让 Viper 读取环境变量 ---
	// 1. 将配置中的点号 (.) 替换为下划线 (_)
	//    例如 config.yaml 里的 database.mongo_uri 会对应环境变量 DATABASE_MONGO_URI
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	viper.AutomaticEnv()
	// ----------------------------------

	if err := viper.ReadInConfig(); err != nil {
		fmt.Printf("Warning: config file not found (%s), utilizing environment variables\n", err)
	}

	// 将配置反序列化到 Conf 结构体中
	if err := viper.Unmarshal(&Conf); err != nil {
		panic(fmt.Errorf("unable to decode into struct, %v", err))
	}

	fmt.Println("Configuration loaded successfully!")
	fmt.Printf("TCP Port from config: %d\n", Conf.Server.TcpPort)
}
