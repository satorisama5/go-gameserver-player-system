package storage

import (
	"errors"
	"fmt"

	"unityserverupgrade/internal/storage/inventory"
)

// PurchaseShopItem 扣钱包并入背包。
func PurchaseShopItem(userKey, userName, reqID, itemID string, count int) (balance int64, duplicate bool, err error) {
	if count <= 0 {
		count = 1
	}
	def, ok := inventory.GetItemDef(itemID)
	if !ok {
		return 0, false, errors.New("商品不存在")
	}
	totalCost := def.Cost * int64(count)
	if _, err := EnsureWalletAccount(userKey, userName, DefaultWalletBalance); err != nil {
		return 0, false, err
	}
	balance, duplicate, err = PurchaseWithLedger(userKey, userName, reqID, itemID, totalCost)
	if err != nil {
		return balance, duplicate, err
	}
	if duplicate {
		return balance, true, nil
	}
	if err := inventory.AddItem(userKey, itemID, count); err != nil {
		return balance, false, fmt.Errorf("发货失败: %w", err)
	}
	return balance, false, nil
}
