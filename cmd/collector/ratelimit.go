package main

import (
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// rateLimitMiddleware limits each IP to rps requests/second with a burst of burst.
func rateLimitMiddleware(rps float64, burst int) gin.HandlerFunc {
	type entry struct {
		limiter *rate.Limiter
	}

	var mu sync.Mutex
	clients := make(map[string]*entry)

	limiter := func(ip string) *rate.Limiter {
		mu.Lock()
		defer mu.Unlock()
		if e, ok := clients[ip]; ok {
			return e.limiter
		}
		l := rate.NewLimiter(rate.Limit(rps), burst)
		clients[ip] = &entry{limiter: l}
		return l
	}

	return func(c *gin.Context) {
		ip := c.ClientIP()
		if !limiter(ip).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "rate limit exceeded"})
			return
		}
		c.Next()
	}
}
