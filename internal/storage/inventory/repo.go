package inventory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var coll *mongo.Collection

// Init 绑定 Mongo 集合（由 storage.InitDB 调用）。
func Init(c *mongo.Collection) error {
	coll = c
	return EnsureIndexes()
}

func EnsureIndexes() error {
	if coll == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := coll.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_key", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("idx_inventory_user_key"),
	})
	return err
}

type Entry struct {
	ItemID   string `json:"item_id" bson:"item_id"`
	Count    int    `json:"count" bson:"count"`
	Equipped bool   `json:"equipped,omitempty" bson:"equipped,omitempty"`
}

type UserInventory struct {
	UserKey string  `bson:"user_key"`
	Items   []Entry `bson:"items"`
	Updated int64   `bson:"updated_at"`
}

func load(userKey string) (*UserInventory, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var inv UserInventory
	err := coll.FindOne(ctx, bson.M{"user_key": userKey}).Decode(&inv)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return &UserInventory{UserKey: userKey, Items: []Entry{}}, nil
		}
		return nil, err
	}
	return &inv, nil
}

func save(inv *UserInventory) error {
	inv.Updated = time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := coll.UpdateOne(ctx,
		bson.M{"user_key": inv.UserKey},
		bson.M{"$set": inv},
		options.Update().SetUpsert(true),
	)
	return err
}

func GetJSON(userKey string) (string, error) {
	inv, err := load(userKey)
	if err != nil {
		return "[]", err
	}
	b, err := json.Marshal(inv.Items)
	return string(b), err
}

func AddItem(userKey, itemID string, count int) error {
	if count <= 0 {
		return errors.New("数量须为正")
	}
	inv, err := load(userKey)
	if err != nil {
		return err
	}
	for i := range inv.Items {
		if inv.Items[i].ItemID == itemID {
			inv.Items[i].Count += count
			return save(inv)
		}
	}
	inv.Items = append(inv.Items, Entry{ItemID: itemID, Count: count})
	return save(inv)
}

func CountItem(userKey, itemID string) (int, error) {
	inv, err := load(userKey)
	if err != nil {
		return 0, err
	}
	for _, it := range inv.Items {
		if it.ItemID == itemID {
			return it.Count, nil
		}
	}
	return 0, nil
}

func ConsumeItem(userKey, itemID string, count int) error {
	inv, err := load(userKey)
	if err != nil {
		return err
	}
	for i := range inv.Items {
		if inv.Items[i].ItemID == itemID {
			if inv.Items[i].Count < count {
				return errors.New("背包数量不足")
			}
			inv.Items[i].Count -= count
			if inv.Items[i].Count == 0 {
				inv.Items = append(inv.Items[:i], inv.Items[i+1:]...)
			}
			return save(inv)
		}
	}
	return errors.New("背包中没有该物品")
}

func SetEquipped(userKey, itemID string) error {
	def, ok := GetItemDef(itemID)
	if !ok || def.Type != "equip" {
		return errors.New("不可装备该物品")
	}
	n, err := CountItem(userKey, itemID)
	if err != nil {
		return err
	}
	if n < 1 {
		return errors.New("背包中没有该装备")
	}
	inv, err := load(userKey)
	if err != nil {
		return err
	}
	for i := range inv.Items {
		d, _ := GetItemDef(inv.Items[i].ItemID)
		if d.Type == "equip" {
			inv.Items[i].Equipped = false
		}
	}
	for i := range inv.Items {
		if inv.Items[i].ItemID == itemID {
			inv.Items[i].Equipped = true
			return save(inv)
		}
	}
	return errors.New("背包中没有该装备")
}

func GetEquippedAtkBonus(userKey string) int64 {
	inv, err := load(userKey)
	if err != nil {
		return 0
	}
	var bonus int64
	for _, it := range inv.Items {
		if !it.Equipped {
			continue
		}
		if d, ok := GetItemDef(it.ItemID); ok {
			bonus += d.AtkBonus
		}
	}
	return bonus
}
