package uploads

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/tus/tusd/v2/pkg/handler"
)

// lockTTL acota la espera si una réplica muere con el lock tomado.
// Debe superar AcquireLockTimeout de tusd (20s) con margen.
const lockTTL = 60 * time.Second

// refreshInterval renueva el TTL mientras el lock está tomado
// (un PATCH puede durar más que lockTTL).
const refreshInterval = 10 * time.Second

// RedisLocker implementa handler.Locker con SET NX + TTL, para que el
// API escale horizontalmente con TUS embebido.
// Aproximación consciente vs memorylocker: el callback requestUnlock
// cross-proceso no existe; el contendiente espera a release o TTL (poll).
// Seguridad: el token propio evita liberar locks ajenos.
type RedisLocker struct {
	RDB *redis.Client
}

// UseIn registra el locker en el composer de tusd.
func (l *RedisLocker) UseIn(c *handler.StoreComposer) {
	c.UseLocker(l)
}

// NewLock crea un lock desbloqueado para el upload dado.
func (l *RedisLocker) NewLock(id string) (handler.Lock, error) {
	return &redisLock{rdb: l.RDB, key: "tus-lock:" + id}, nil
}

type redisLock struct {
	rdb  *redis.Client
	key  string
	val  string
	stop chan struct{}
}

func newToken() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "fallback"
	}
	return hex.EncodeToString(b[:])
}

// Lock adquiere el lock exclusivo (poll cada 100ms) o retorna
// handler.ErrLockTimeout si el contexto expira. Renueva el TTL en
// segundo plano mientras lo mantiene.
func (l *redisLock) Lock(ctx context.Context, _ func()) error {
	l.val = newToken()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := l.rdb.SetNX(ctx, l.key, l.val, lockTTL).Result()
		if err != nil {
			return err
		}
		if ok {
			l.stop = make(chan struct{})
			go l.refresh()
			return nil
		}
		select {
		case <-ctx.Done():
			return handler.ErrLockTimeout
		case <-ticker.C:
		}
	}
}

// Unlock detiene la renovación y libera solo si el token es propio (Lua).
func (l *redisLock) Unlock() error {
	if l.stop != nil {
		select {
		case <-l.stop:
		default:
			close(l.stop)
		}
	}
	script := redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`)
	return script.Run(context.Background(), l.rdb, []string{l.key}, l.val).Err()
}

// refresh extiende el TTL solo mientras el token siga siendo propio.
func (l *redisLock) refresh() {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	script := redis.NewScript(`if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`)
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			_ = script.Run(context.Background(), l.rdb, []string{l.key}, l.val, int(lockTTL.Milliseconds())).Err()
		}
	}
}
