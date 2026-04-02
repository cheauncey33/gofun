package common

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var MySecret = []byte("whu-snack-go")

type MyClaims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// 用户登录成功后调用 给用户生成返回一个有效期为500s的jwt
func GenerateToken(userID int64) (string, error) {
	claims := MyClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(500 * time.Second)),
			Issuer:    "zyh",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(MySecret)
}

// 对传入的jwt鉴伪
func ParseToken(tokenString string) (*MyClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &MyClaims{}, func(t *jwt.Token) (any, error) {
		return MySecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*MyClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}
