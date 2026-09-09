// 本地演示库一键清空（升级5 迁移后使用）。
//
// 用法（项目根目录）:
//
//	go run ./cmd/cleardb
package main

import (
	"log"

	cfg "unityserverupgrade/internal/config"
	"unityserverupgrade/internal/storage"
	"unityserverupgrade/internal/tool"
)

func main() {
	cfg.InitConfig()
	storage.InitRedis()
	storage.InitDB()

	if err := tool.ClearDevDatabase(); err != nil {
		log.Fatal(err)
	}
}
