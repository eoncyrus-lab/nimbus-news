package discord

import (
	"math/rand"
	"sync"
	"time"
)

type TokenBucket struct {
	capacity   int
	tokens     int
	refillRate time.Duration
	lastRefill time.Time
	mu         sync.Mutex
}

func NewTokenBucket(capacity int, refillRate time.Duration) *TokenBucket {
	return &TokenBucket{
		capacity:   capacity,
		tokens:     capacity,
		refillRate: refillRate,
		lastRefill: time.Now(),
	}
}

func (b *TokenBucket) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens > 0 {
		b.tokens--
		return true
	}
	return false
}

func (b *TokenBucket) AllowN(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	if b.tokens >= n {
		b.tokens -= n
		return true
	}
	return false
}

func (b *TokenBucket) refill() {
	now := time.Now()
	elapsed := now.Sub(b.lastRefill)
	tokensToAdd := int(elapsed / b.refillRate)
	if tokensToAdd > 0 {
		b.tokens = min(b.capacity, b.tokens+tokensToAdd)
		b.lastRefill = now
	}
}

func (b *TokenBucket) Available() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refill()
	return b.tokens
}

func (b *TokenBucket) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens = b.capacity
	b.lastRefill = time.Now()
}

type DiscordRatelimiter struct {
	messageBucket *TokenBucket
	burstMax      int
}

func NewDiscordRatelimiter(_ int, msgPerSec, burstMax int) *DiscordRatelimiter {
	return &DiscordRatelimiter{
		messageBucket: NewTokenBucket(msgPerSec, time.Second/time.Duration(msgPerSec)),
		burstMax:      burstMax,
	}
}

func (r *DiscordRatelimiter) AllowMessage() bool {
	return r.messageBucket.Allow() || r.messageBucket.AllowN(r.burstMax)
}

func (r *DiscordRatelimiter) NextRetryDelay(_ string, attempts int) time.Duration {
	base := time.Second * 15
	maxDelay := time.Minute * 5

	delay := base * time.Duration(1<<min(attempts, 5))
	if delay > maxDelay {
		delay = maxDelay
	}

	jitter := time.Duration(rand.Int63n(int64(delay/2)) + int64(delay/2))
	return jitter
}
