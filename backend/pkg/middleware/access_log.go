package middleware

import (
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gofun/pkg/logger"
	"net/http"
	"syscall"
	"time"
)

func AccessLogMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		started := time.Now()
		c.Next()
		fields := []zap.Field{
			zap.String("method", c.Request.Method), zap.String("path", c.Request.URL.Path),
			zap.String("route", c.FullPath()), zap.Int("status", c.Writer.Status()),
			zap.Float64("latency_ms", float64(time.Since(started).Microseconds())/1000),
		}
		if id, ok := c.Get("user_id"); ok {
			fields = append(fields, zap.Any("user_id", id))
		}
		log := logger.FromContext(c.Request.Context())
		switch {
		case c.Writer.Status() >= 500:
			log.Error("HTTP 请求完成", fields...)
		case c.Writer.Status() >= 400:
			log.Warn("HTTP 请求完成", fields...)
		default:
			log.Info("HTTP 请求完成", fields...)
		}
	}
}

// Recovery logs no headers or bodies; credentials must never enter panic logs.
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if value := recover(); value != nil {
				if err, ok := value.(error); ok {
					if errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
						c.Abort()
						return
					}
				}
				logger.FromContext(c.Request.Context()).Error("HTTP 请求 panic", zap.String("panic", fmt.Sprint(value)), zap.Stack("panic_stack"))
				c.AbortWithStatus(http.StatusInternalServerError)
			}
		}()
		c.Next()
	}
}
