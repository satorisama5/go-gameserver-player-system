package session

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"unityserverupgrade/internal/storage"
	"unityserverupgrade/internal/storage/inventory"
)

// HandleShopList：shop
func (u *User) HandleShopList(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	json, err := inventory.ListShopItemsJSON()
	if err != nil {
		u.Send(PackTextMessage("商城列表失败: " + err.Error()))
		return true
	}
	u.sendTextAndReqDone("SHOP_LIST|"+json, "shop")
	return true
}

// HandleBuy：buy|item_id|数量(可选)
func (u *User) HandleBuy(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	parts := strings.Split(args, "|")
	if len(parts) < 1 || strings.TrimSpace(parts[0]) == "" {
		u.Send(PackTextMessage("格式: buy|item_id|数量(可选)"))
		return true
	}
	itemID := strings.TrimSpace(parts[0])
	count := 1
	if len(parts) > 1 {
		fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &count)
	}
	reqID := u.currentReqID
	if reqID == "" {
		reqID = "buy_" + itemID + "_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	balance, dup, err := storage.PurchaseShopItem(u.DbKey, u.Name, reqID, itemID, count)
	if err != nil {
		u.Send(PackTextMessage("购买失败: " + err.Error()))
		return true
	}
	if dup {
		u.sendTextAndReqDone(fmt.Sprintf("SHOP_BUY_DUPLICATE|item=%s|balance=%d", itemID, balance), "buy")
		return true
	}
	u.sendTextAndReqDone(fmt.Sprintf("SHOP_BUY_OK|item=%s|count=%d|balance=%d", itemID, count, balance), "buy")
	return true
}
