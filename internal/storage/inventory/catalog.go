package inventory

import "encoding/json"

// ItemDef 商城/背包配置（演示用内存表，可后续改 JSON 热更）。
type ItemDef struct {
	ItemID   string `json:"item_id"`
	Name     string `json:"name"`
	Type     string `json:"type"` // consumable | equip
	Cost     int64  `json:"cost"`
	HealHP   int64  `json:"heal_hp,omitempty"`
	AtkBonus int64  `json:"atk_bonus,omitempty"`
}

var DefaultCatalog = map[string]ItemDef{
	"potion_hp": {ItemID: "potion_hp", Name: "生命药水", Type: "consumable", Cost: 50, HealHP: 40},
	"sword_01":  {ItemID: "sword_01", Name: "铁剑", Type: "equip", Cost: 200, AtkBonus: 10},
	"armor_01":  {ItemID: "armor_01", Name: "皮甲", Type: "equip", Cost: 150, AtkBonus: 0},
}

func GetItemDef(itemID string) (ItemDef, bool) {
	d, ok := DefaultCatalog[itemID]
	return d, ok
}

func ListShopItemsJSON() (string, error) {
	list := make([]ItemDef, 0, len(DefaultCatalog))
	for _, d := range DefaultCatalog {
		list = append(list, d)
	}
	b, err := json.Marshal(list)
	return string(b), err
}
