package world

import (
	"math"
)

const (
	DefaultEloRating = 1000
	DefaultEloK      = 32.0
)

// EloExpected A 对 B 的期望胜率。
func EloExpected(ratingA, ratingB float64) float64 {
	return 1.0 / (1.0 + math.Pow(10, (ratingB-ratingA)/400.0))
}

// EloUpdate 按实际战果更新双方积分；scoreA: 1 胜 / 0 负 / 0.5 平。
func EloUpdate(ratingA, ratingB, scoreA, k float64) (newA, newB float64) {
	if k <= 0 {
		k = DefaultEloK
	}
	ea := EloExpected(ratingA, ratingB)
	eb := EloExpected(ratingB, ratingA)
	newA = ratingA + k*(scoreA-ea)
	newB = ratingB + k*((1.0-scoreA)-eb)
	return newA, newB
}

// LevelFromRating 用 rating 映射展示等级（简单分段）。
func LevelFromRating(rating int) int {
	if rating < 800 {
		return 1
	}
	lv := 1 + (rating-800)/100
	if lv < 1 {
		return 1
	}
	if lv > 50 {
		return 50
	}
	return lv
}
