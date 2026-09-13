package auth

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

type visitor struct {
	lim  *rate.Limiter
	last time.Time
}

// visitors limita por IP en memoria de la instancia.
var visitors = struct {
	sync.Mutex
	m map[string]*visitor
}{m: make(map[string]*visitor)}

// RateLimit limita por IP a rps con burst dado. Responde 429 al exceder.
func RateLimit(rps float64, burst int) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		visitors.Lock()
		v, ok := visitors.m[ip]
		if !ok {
			v = &visitor{lim: rate.NewLimiter(rate.Limit(rps), burst)}
			visitors.m[ip] = v
		}
		v.last = time.Now()
		visitors.Unlock()
		if !v.lim.Allow() {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "límite de peticiones excedido"})
			c.Abort()
			return
		}
		c.Next()
	}
}
