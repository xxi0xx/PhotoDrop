// Package abuse provides bounded, process-local admission controls.
package abuse

import (
	"crypto/sha256"
	"math"
	"sync"
	"time"
)

type bucket struct {
	tokens            float64
	updated, lastSeen time.Time
}
type Limiter struct {
	mu       sync.Mutex
	entries  map[[32]byte]bucket
	capacity int
	now      func() time.Time
	swept    time.Time
}

func NewLimiter(capacity int) *Limiter {
	return &Limiter{entries: make(map[[32]byte]bucket), capacity: capacity, now: time.Now}
}

// Allow uses a token bucket: rate tokens/minute and an equal initial burst.
// Capacity pressure rejects new keys rather than evicting live throttled keys.
// Raw identifiers are not retained, logged, or persisted.
func (l *Limiter) Allow(key string, rate int) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.swept) >= time.Minute {
		for k, b := range l.entries {
			if now.Sub(b.lastSeen) >= 10*time.Minute {
				delete(l.entries, k)
			}
		}
		l.swept = now
	}
	hash := sha256.Sum256([]byte(key))
	b, ok := l.entries[hash]
	if !ok {
		if len(l.entries) >= l.capacity {
			return false, 60
		}
		b = bucket{tokens: float64(rate), updated: now}
	}
	elapsed := now.Sub(b.updated).Seconds()
	if elapsed < 0 {
		elapsed = 0
	}
	b.tokens = math.Min(float64(rate), b.tokens+elapsed*float64(rate)/60)
	b.updated, b.lastSeen = now, now
	if b.tokens >= 1 {
		b.tokens--
		l.entries[hash] = b
		return true, 0
	}
	l.entries[hash] = b
	return false, max(1, int(math.Ceil((1-b.tokens)*60/float64(rate))))
}
