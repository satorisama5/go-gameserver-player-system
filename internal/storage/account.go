package storage

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/crypto/bcrypt"
)

var AccountCollection *mongo.Collection

// AccountModel 账号表：登录凭据 + 稳定 user_id（全服 DbKey）。
type AccountModel struct {
	UserID       string `bson:"user_id"`
	Account      string `bson:"account"`
	PasswordHash string `bson:"password_hash"`
	DisplayName  string `bson:"display_name"`
	CreatedAt    int64  `bson:"created_at"`
	UpdatedAt    int64  `bson:"updated_at"`
}

const bcryptCost = 10

func EnsureAccountIndexes() error {
	if AccountCollection == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "account", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_accounts_account_unique"),
		},
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_accounts_user_id_unique"),
		},
		{
			Keys:    bson.D{{Key: "display_name", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("idx_accounts_display_name_unique"),
		},
	}
	_, err := AccountCollection.Indexes().CreateMany(ctx, models)
	return err
}

func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func CheckPassword(hash, plain string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain)) == nil
}

// CreateAccount 注册新账号；account / displayName 均需全局唯一。
func CreateAccount(account, password, displayName string) (*AccountModel, error) {
	account = normalizeAccount(account)
	displayName = stringsTrim(displayName)
	if account == "" || password == "" || displayName == "" {
		return nil, errors.New("账号、密码、昵称均不能为空")
	}
	if len(password) < 6 {
		return nil, errors.New("密码至少 6 位")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}

	now := time.Now().Unix()
	doc := AccountModel{
		UserID:       uuid.NewString(),
		Account:      account,
		PasswordHash: hash,
		DisplayName:  displayName,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err = AccountCollection.InsertOne(ctx, doc)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return nil, errors.New("账号或昵称已被占用")
		}
		return nil, err
	}
	return &doc, nil
}

func FindAccountByLogin(account string) (*AccountModel, error) {
	account = normalizeAccount(account)
	if account == "" {
		return nil, errors.New("账号不能为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc AccountModel
	err := AccountCollection.FindOne(ctx, bson.M{"account": account}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("账号或密码错误")
		}
		return nil, err
	}
	return &doc, nil
}

func FindAccountByUserID(userID string) (*AccountModel, error) {
	if userID == "" {
		return nil, errors.New("user_id 为空")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var doc AccountModel
	err := AccountCollection.FindOne(ctx, bson.M{"user_id": userID}).Decode(&doc)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("用户不存在")
		}
		return nil, err
	}
	return &doc, nil
}

// UpdateDisplayName 修改游戏内昵称（需已登录）。
func UpdateDisplayName(userID, newDisplayName string) error {
	newDisplayName = stringsTrim(newDisplayName)
	if userID == "" || newDisplayName == "" {
		return errors.New("昵称不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := AccountCollection.UpdateOne(ctx,
		bson.M{"user_id": userID},
		bson.M{"$set": bson.M{"display_name": newDisplayName, "updated_at": time.Now().Unix()}},
	)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) {
			return errors.New("昵称已被占用")
		}
		return err
	}
	if res.MatchedCount == 0 {
		return errors.New("用户不存在")
	}
	return nil
}

func normalizeAccount(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func stringsTrim(s string) string {
	return strings.TrimSpace(s)
}
