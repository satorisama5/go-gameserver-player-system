// internal/config.go
package internal

import (
	"fmt"
	"github.com/spf13/viper"
	"strings"
)

// Config 对应 config.yaml 的顶级结构
var Conf Config

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	Database DatabaseConfig `mapstructure:"database"`
	Cache    CacheConfig    `mapstructure:"cache"`
	Log      LogConfig      `mapstructure:"log"`
	AOI      AOIConfig      `mapstructure:"aoi"`
	MQ       MQConfig       `mapstructure:"mq"`
}

type ServerConfig struct {
	TcpPort             int            `mapstructure:"tcp_port"`
	GrpcPort            int            `mapstructure:"grpc_port"`
	WsProxyPort         int            `mapstructure:"ws_proxy_port"`
	MaxConnections      int            `mapstructure:"max_connections"`
	MaxPacketsPerSecond int            `mapstructure:"max_packets_per_second"`
	MaxPlayerSpeed      float64        `mapstructure:"max_player_speed"`
	SpawnPoint          PlayerPosition `mapstructure:"spawn_point"`
}
type MQConfig struct {
	Url       string `mapstructure:"url"`
	QueueName string `mapstructure:"queue_name"`
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
