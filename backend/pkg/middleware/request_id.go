package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"gofun/pkg/logger"
)

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = uuid.New().String()
		}
		c.Set("request_id", rid)
		c.Request = c.Request.WithContext(logger.WithRequestID(c.Request.Context(), rid))
		c.Header("X-Request-ID", rid)
		c.Next()
	}
}
