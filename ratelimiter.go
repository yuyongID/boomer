package boomer

import (
	"log"
	"math"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// runner uses a rate limiter to put limits on task executions.
type rateLimiter interface {
	start()
	acquire() bool
	stop()
}

// stableRateLimiter uses the token bucket algorithm.
// the bucket is refilled according to the refill period, no burst is allowed.
type stableRateLimiter struct {
	threshold        int64
	currentThreshold int64
	refillPeroid     time.Duration
	broadcastChannel chan bool
	quitChannel      chan bool
}

func newStableRateLimiter(threshold int64, refillPeroid time.Duration) (rateLimiter *stableRateLimiter) {
	rateLimiter = &stableRateLimiter{
		threshold:        threshold,
		currentThreshold: threshold,
		refillPeroid:     refillPeroid,
		broadcastChannel: make(chan bool),
	}
	return rateLimiter
}

func (limiter *stableRateLimiter) start() {
	limiter.quitChannel = make(chan bool)
	quitChannel := limiter.quitChannel
	go func() {
		for {
			select {
			case <-quitChannel:
				return
			default:
				atomic.StoreInt64(&limiter.currentThreshold, limiter.threshold)
				time.Sleep(limiter.refillPeroid)
				close(limiter.broadcastChannel)
				limiter.broadcastChannel = make(chan bool)
			}
		}
	}()
}

func (limiter *stableRateLimiter) acquire() (blocked bool) {
	permit := atomic.AddInt64(&limiter.currentThreshold, -1)
	if permit < 0 {
		blocked = true
		// block until the bucket is refilled
		<-limiter.broadcastChannel
	} else {
		blocked = false
	}
	return blocked
}

func (limiter *stableRateLimiter) stop() {
	close(limiter.quitChannel)
}

// warmUpRateLimiter uses the token bucket algorithm.
// the threshold is updated according to the warm up rate.
// the bucket is refilled according to the refill period, no burst is allowed.
type warmUpRateLimiter struct {
	maxThreshold     int64
	nextThreshold    int64
	currentThreshold int64
	refillPeroid     time.Duration
	warmUpRate       string
	warmUpStep       int64
	warmUpPeroid     time.Duration
	broadcastChannel chan bool
	warmUpChannel    chan bool
	quitChannel      chan bool
}

func newWarmUpRateLimiter(maxThreshold int64, warmUpRate string, refillPeroid time.Duration) (rateLimiter *warmUpRateLimiter) {
	rateLimiter = &warmUpRateLimiter{
		maxThreshold:     maxThreshold,
		nextThreshold:    0,
		currentThreshold: 0,
		warmUpRate:       warmUpRate,
		refillPeroid:     refillPeroid,
		broadcastChannel: make(chan bool),
	}
	rateLimiter.warmUpStep, rateLimiter.warmUpPeroid = rateLimiter.parseWarmUpRate(rateLimiter.warmUpRate)
	return rateLimiter
}

func (limiter *warmUpRateLimiter) parseWarmUpRate(warmUpRate string) (int64, time.Duration) {
	if strings.Contains(warmUpRate, "/") {
		tmp := strings.Split(warmUpRate, "/")
		if len(tmp) != 2 {
			log.Fatalf("Wrong format of warmUpRate, %s", warmUpRate)
		}
		warmUpStep, err := strconv.ParseInt(tmp[0], 10, 64)
		if err != nil {
			log.Fatalf("Failed to parse warmUpRate, %v", err)
		}
		warmUpPeroid, err := time.ParseDuration(tmp[1])
		if err != nil {
			log.Fatalf("Failed to parse warmUpRate, %v", err)
		}
		return warmUpStep, warmUpPeroid
	}

	warmUpStep, err := strconv.ParseInt(warmUpRate, 10, 64)
	if err != nil {
		log.Fatalf("Failed to parse warmUpRate, %v", err)
	}
	warmUpPeroid := time.Second
	return warmUpStep, warmUpPeroid
}

func (limiter *warmUpRateLimiter) start() {
	limiter.quitChannel = make(chan bool)
	quitChannel := limiter.quitChannel
	// bucket updater
	go func() {
		for {
			select {
			case <-quitChannel:
				return
			default:
				atomic.StoreInt64(&limiter.currentThreshold, limiter.nextThreshold)
				time.Sleep(limiter.refillPeroid)
				close(limiter.broadcastChannel)
				limiter.broadcastChannel = make(chan bool)
			}
		}
	}()
	// threshold updater
	go func() {
		for {
			select {
			case <-quitChannel:
				return
			default:
				limiter.nextThreshold = limiter.nextThreshold + limiter.warmUpStep
				if limiter.nextThreshold < 0 {
					// int64 overflow
					limiter.nextThreshold = int64(math.MaxInt64)
				}
				if limiter.nextThreshold > limiter.maxThreshold {
					limiter.nextThreshold = limiter.maxThreshold
				}
				time.Sleep(limiter.warmUpPeroid)
			}
		}
	}()
}

func (limiter *warmUpRateLimiter) acquire() (blocked bool) {
	permit := atomic.AddInt64(&limiter.currentThreshold, -1)
	if permit < 0 {
		blocked = true
		// block until the bucket is refilled
		<-limiter.broadcastChannel
	} else {
		blocked = false
	}
	return blocked
}

func (limiter *warmUpRateLimiter) stop() {
	limiter.nextThreshold = 0
	close(limiter.quitChannel)
}
