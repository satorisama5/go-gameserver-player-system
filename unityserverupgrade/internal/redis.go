// redis.go
package internal

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
)

var RDB *redis.Client
var Ctx = context.Background()

func InitRedis() {
	addr := Conf.Cache.RedisAddr
	
	RDB = redis.NewClient(&redis.Options{
		Addr:     addr, // Redis 服务器地址
		Password: "",   // 没有密码，填空
		DB:       0,    // 使用默认 DB
	})

	// 检查连接
	_, err := RDB.Ping(Ctx).Result()
	if err != nil {
		fmt.Println("Redis 连接失败:", err)
		panic(err)
	}

	fmt.Println("Redis 连接成功！")
}
