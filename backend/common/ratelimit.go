package common

import (
	"WHU_Snack_GO/pkg/response"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func GlobalRateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	limiter := rate.NewLimiter(r, b)
	return func(c *gin.Context) {
		if !limiter.Allow() {
			response.Error(c, http.StatusTooManyRequests, response.CodeTooManyRequests, "系统繁忙")
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

// Cleanup removes stale entries from the IP map
func (i *IPRateLimiter) Cleanup() {
	i.mu.Lock()
	defer i.mu.Unlock()
	// reset the map: old entries get GC'd, new entries populate on demand
	if len(i.ips) > 100000 {
		i.ips = make(map[string]*rate.Limiter)
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

var globalIPLimiter *IPRateLimiter

func CleanupIPLimiters() {
	if globalIPLimiter != nil {
		globalIPLimiter.Cleanup()
	}
}

func IPRateLimitMiddleware(r rate.Limit, b int) gin.HandlerFunc {
	i := NewIPRateLimiter(r, b)
	globalIPLimiter = i
	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !i.GetLimiter(ip).Allow() {
			response.Error(c, http.StatusTooManyRequests, response.CodeTooManyRequests, "访问过于频繁")
			c.Abort()
			return
		}
		c.Next()
	}
}
