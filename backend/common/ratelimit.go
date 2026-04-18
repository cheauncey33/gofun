package common

import (
	"fmt"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func GlobalRateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	limiter := rate.NewLimiter(r, b)
	return func(c *gin.Context) {
		if !limiter.Allow() {
			c.JSON(429, gin.H{"msg": "系统繁忙"})
			c.Abort()
			return
		}
		c.Next()
	}
}

type IPRateLimiter struct {
	ips map[string]*rate.Limiter
	mu  *sync.RWMutex
	r   rate.Limit
	b   int
}

func NewIPRateLimiter(r rate.Limit, b int) *IPRateLimiter {
	return &IPRateLimiter{
		ips: make(map[string]*rate.Limiter),
		mu:  &sync.RWMutex{},
		r:   r,
		b:   b,
	}
}
func (i *IPRateLimiter) GetLimiter(ip string) *rate.Limiter {
	i.mu.RLock()
	limiter, ok := i.ips[ip]
	i.mu.RUnlock()

	if !ok {
		i.mu.Lock()

		limiter, ok = i.ips[ip]
		if !ok {
			limiter = rate.NewLimiter(i.r, i.b)
			i.ips[ip] = limiter
		}
		i.mu.Unlock()
	}
	return limiter
}

func IPRateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	i := NewIPRateLimiter(r, b)
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !i.GetLimiter(ip).Allow() {
			fmt.Printf("ip限流 IP：%s\n", ip)
			c.JSON(429, gin.H{
				"code": 429,
				"msg":  "访问过于频繁",
			})
			c.Abort()
			return
		}
		c.Next()
	}
}
