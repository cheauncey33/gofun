package common

import (
	"gofun/models"
	"gofun/pkg/response"
	"net/http"

	"github.com/gin-gonic/gin"
)

func AdminAuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := c.Get("user_id")
		if !exists {
			response.Error(c, http.StatusForbidden, response.CodeForbidden, "管理员权限不足")
			c.Abort()
			return
		}

		var user models.User
		if err := DB.Select("role").Where("id = ?", userID).First(&user).Error; err != nil {
			response.Error(c, http.StatusForbidden, response.CodeForbidden, "管理员权限不足")
			c.Abort()
			return
		}

		if user.Role != "admin" {
			response.Error(c, http.StatusForbidden, response.CodeForbidden, "管理员权限不足")
			c.Abort()
			return
		}

		c.Next()
	}
}
