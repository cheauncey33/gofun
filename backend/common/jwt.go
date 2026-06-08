package common

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var JWTSecret []byte

// SetJWTSecret 在启动时从config初始化JWT密钥
func SetJWTSecret(secret string) {
	JWTSecret = []byte(secret)
}

type MyClaims struct {
	UserID    int64  `json:"user_id"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

// GenerateToken 用户登录成功后调用，生成有效期为expireSecs秒的jwt
func GenerateToken(userID int64, expireSecs int) (string, error) {
	claims := MyClaims{
		UserID:    userID,
		TokenType: "access",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(expireSecs) * time.Second)),
			Issuer:    "zyh",
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(JWTSecret)
}

// ParseToken 对传入的jwt鉴伪
func ParseToken(tokenString string) (*MyClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &MyClaims{}, func(t *jwt.Token) (any, error) {
		return JWTSecret, nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(*MyClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}
