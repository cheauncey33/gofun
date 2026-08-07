package common

import (
	"gofun/pkg/response"
	"net/http"

	"github.com/gin-gonic/gin"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
			c.Abort()
			return
		}
		claims, err := ParseToken(tokenString)
		if err != nil {
			response.Error(c, http.StatusUnauthorized, response.CodeTokenInvalid, "token无效或已过期")
			c.Abort()
			return
		}
		if claims.TokenType != "" && claims.TokenType != "access" {
			response.Error(c, http.StatusUnauthorized, response.CodeTokenInvalid, "token类型无效")
			c.Abort()
			return
		}
		c.Set("user_id", claims.UserID)
		c.Next()
	}
}

// OptionalAuthMiddleware 有合法 access token 时写入 user_id，否则继续匿名访问。
func OptionalAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString := c.GetHeader("Authorization")
		if tokenString == "" {
			c.Next()
			return
		}
		claims, err := ParseToken(tokenString)
		if err == nil && (claims.TokenType == "" || claims.TokenType == "access") {
			c.Set("user_id", claims.UserID)
		}
		c.Next()
	}
}

// GetUserID safely extracts user_id from gin context. Returns 0, false if not set.
func GetUserID(c *gin.Context) (int64, bool) {
	v, ok := c.Get("user_id")
	if !ok {
		return 0, false
	}
	id, ok := v.(int64)
	return id, ok
}
