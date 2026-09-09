package session

import (
	"fmt"
	"strings"

	"unityserverupgrade/internal/storage/inventory"
)

// HandleInventory：inventory
func (u *User) HandleInventory(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	json, err := inventory.GetJSON(u.DbKey)
	if err != nil {
		u.Send(PackTextMessage("背包读取失败: " + err.Error()))
		return true
	}
	u.sendTextAndReqDone("INVENTORY|"+json, "inventory")
	return true
}

// HandleUseItem：use|item_id
func (u *User) HandleUseItem(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	itemID := strings.TrimSpace(args)
	if itemID == "" {
		u.Send(PackTextMessage("格式: use|item_id"))
		return true
	}
	def, ok := inventory.GetItemDef(itemID)
	if !ok {
		u.Send(PackTextMessage("未知物品"))
		return true
	}
	if def.Type != "consumable" {
		u.Send(PackTextMessage("该物品不可使用"))
		return true
	}
	if err := inventory.ConsumeItem(u.DbKey, itemID, 1); err != nil {
		u.Send(PackTextMessage("使用失败: " + err.Error()))
		return true
	}
	// 演示：仅回执；HP 恢复可后续接 BehaviorManager
	u.sendTextAndReqDone(fmt.Sprintf("ITEM_USE_OK|item=%s|heal=%d", itemID, def.HealHP), "use")
	return true
}

// HandleEquipItem：equip|item_id
func (u *User) HandleEquipItem(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	itemID := strings.TrimSpace(args)
	if itemID == "" {
		u.Send(PackTextMessage("格式: equip|item_id"))
		return true
	}
	if err := inventory.SetEquipped(u.DbKey, itemID); err != nil {
		u.Send(PackTextMessage("装备失败: " + err.Error()))
		return true
	}
	bonus := inventory.GetEquippedAtkBonus(u.DbKey)
	u.sendTextAndReqDone(fmt.Sprintf("EQUIP_OK|item=%s|atk_bonus=%d", itemID, bonus), "equip")
	return true
}

// HandleMobList：mobs（房内怪物名列表）
func (u *User) HandleMobList(args string) bool {
	if u.Room == nil {
		u.Send(PackTextMessage("你不在任何房间中"))
		return true
	}
	u.sendTextAndReqDone("MOB_LIST|"+u.Room.MobListJSON(), "mobs")
	return true
}
