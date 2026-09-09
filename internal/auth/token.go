package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	config "unityserverupgrade/internal/config"
)

// Claims 写入 JWT 的载荷；UserID 即全服稳定的 DbKey / user_key。
type Claims struct {
	UserID      string `json:"uid"`
	Account     string `json:"acct"`
	DisplayName string `json:"dn"`
	jwt.RegisteredClaims
}

func secret() []byte {
	s := config.Conf.Auth.JWTSecret
	if s == "" {
		s = "unityserverupgrade-dev-secret-change-me"
	}
	return []byte(s)
}

func tokenTTL() time.Duration {
	h := config.Conf.Auth.TokenTTLHours
	if h <= 0 {
		h = 168
	}
	return time.Duration(h) * time.Hour
}

// IssueToken 签发登录令牌。
func IssueToken(userID, account, displayName string) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID:      userID,
		Account:     account,
		DisplayName: displayName,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(tokenTTL())),
			Issuer:    "unityserverupgrade",
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString(secret())
}

// ParseToken 校验并解析令牌；失败返回 error。
func ParseToken(tokenStr string) (*Claims, error) {
	if tokenStr == "" {
		return nil, errors.New("empty token")
	}
	t, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return secret(), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := t.Claims.(*Claims)
	if !ok || !t.Valid {
		return nil, errors.New("invalid token claims")
	}
	return claims, nil
}
