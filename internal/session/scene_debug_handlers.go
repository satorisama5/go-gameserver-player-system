package session

import (
	"fmt"
	"strconv"
	"strings"

	protocol "unityserverupgrade/internal/protocol"
)

// HandleSceneDebug 场景同步调试：
//
//	scenedebug|demo          单机推演 A/B/C 跨格进出视野（不需多开）
//	scenedebug|now           打印当前房内每人格子与可见名单
//	scenedebug|on            打开约 60 tick（~2s）的每包 SCENEDBG
//	scenedebug|on|N          打开 N 次广播的 SCENEDBG（上限 600）
//	scenedebug|off           关闭
//	warp|x|y|z               强制传送（测跨格 / MarkAOIResync）；可省略 y 用 0
func (u *User) HandleSceneDebug(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	parts := strings.Split(args, "|")
	op := strings.ToLower(strings.TrimSpace(parts[0]))
	if op == "" {
		u.Send(PackTextMessage("scenedebug|demo|now|on|on|N|off 或 warp|x|y|z"))
		return true
	}

	switch op {
	case "demo":
		if u.Room == nil {
			// 无房也可用任意临时逻辑：需要 AOIManager。提示先建房。
			u.Send(PackTextMessage("SCENEDBG_DEMO|err=join_or_create_room_first"))
			return true
		}
		line := u.Room.SceneDebugDemoABC()
		fmt.Println(line)
		u.Send(PackTextMessage(line))
	case "now", "snap", "status":
		if u.Room == nil {
			u.Send(PackTextMessage("SCENEDBG_SNAP|err=not_in_room"))
			return true
		}
		line := u.Room.SceneDebugSnapshot()
		fmt.Println(line)
		u.Send(PackTextMessage(line))
	case "on":
		if u.Room == nil {
			u.Send(PackTextMessage("SCENEDBG|err=not_in_room"))
			return true
		}
		n := 60
		if len(parts) >= 2 {
			if v, err := strconv.Atoi(strings.TrimSpace(parts[1])); err == nil {
				n = v
			}
		}
		u.Room.EnableSceneDebug(n)
		u.Send(PackTextMessage(fmt.Sprintf("SCENEDBG|on|ticks=%d|hint=move_or_warp_to_see_lines", n)))
	case "off":
		if u.Room != nil {
			u.Room.DisableSceneDebug()
		}
		u.Send(PackTextMessage("SCENEDBG|off"))
	default:
		u.Send(PackTextMessage("scenedebug|demo|now|on|on|N|off"))
	}
	return true
}

// HandleWarp 强制传送：warp|x|z 或 warp|x|y|z
func (u *User) HandleWarp(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	if u.Room == nil {
		u.Send(PackTextMessage("WARP_FAIL|not_in_room"))
		return true
	}
	parts := strings.Split(args, "|")
	if len(parts) < 2 {
		u.Send(PackTextMessage("用法: warp|x|z 或 warp|x|y|z"))
		return true
	}
	var x, y, z float64
	if len(parts) == 2 {
		fmt.Sscanf(strings.TrimSpace(parts[0]), "%f", &x)
		fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &z)
	} else {
		fmt.Sscanf(strings.TrimSpace(parts[0]), "%f", &x)
		fmt.Sscanf(strings.TrimSpace(parts[1]), "%f", &y)
		fmt.Sscanf(strings.TrimSpace(parts[2]), "%f", &z)
	}
	pos := protocol.PlayerPosition{X: float32(x), Y: float32(y), Z: float32(z)}
	u.ForceTeleport(pos)
	gid := u.Room.AOIManager.GetGridIDByPos(pos.X, pos.Z)
	u.Send(PackTextMessage(fmt.Sprintf("WARP_OK|pos=%.1f,%.1f,%.1f|gid=%d", pos.X, pos.Y, pos.Z, gid)))
	return true
}
