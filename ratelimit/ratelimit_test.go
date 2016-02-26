package ratelimit

import (
	"fmt"
	"math/rand"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/tsenart/tb"
)

func TestRateLimit(t *testing.T) {
	if os.Getenv("REDIS_URL") == "" {
		t.Skip("skipping redis test since there is no REDIS_URL")
	}

	if time.Now().Minute() > 58 {
		t.Log("Note: The TestRateLimit test is known to have a bug if run near the top of the hour. Since the rate limiter isn't a moving window, it could end up checking against two different buckets on either side of the top of the hour, so if you see that just re-run it after you've passed the top of the hour.")
	}

	rateLimiter := NewRateLimiter(os.Getenv("REDIS_URL"), fmt.Sprintf("worker-test-rl-%d", os.Getpid()))

	ok, err := rateLimiter.RateLimit("slow", 2, time.Hour)
	if err != nil {
		t.Fatalf("rate limiter error: %v", err)
	}
	if !ok {
		t.Fatal("expected to not get rate limited, but was limited")
	}

	ok, err = rateLimiter.RateLimit("slow", 2, time.Hour)
	if err != nil {
		t.Fatalf("rate limiter error: %v", err)
	}
	if !ok {
		t.Fatal("expected to not get rate limited, but was limited")
	}

	ok, err = rateLimiter.RateLimit("slow", 2, time.Hour)
	if err != nil {
		t.Fatalf("rate limiter error: %v", err)
	}
	if ok {
		t.Fatal("expected to get rate limited, but was not limited")
	}
}

type TimeSlice []time.Time

func (ts TimeSlice) Len() int {
	return len(ts)
}

func (ts TimeSlice) Less(i, j int) bool {
	return ts[i].Before(ts[j])
}

func (ts TimeSlice) Swap(i, j int) {
	ts[i], ts[j] = ts[j], ts[i]
}

func TestRateLimitLoadTest(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping rate limit load test in short mode")
	}
	if os.Getenv("REDIS_URL") == "" {
		t.Skip("skipping redis test since there is no REDIS_URL")
	}

	concurrencyCount := 50
	var rateLimiters []RateLimiter
	maxJitter := 10 * time.Millisecond
	for i := 0; i < concurrencyCount; i++ {
		rateLimiters = append(rateLimiters, NewRateLimiter(os.Getenv("REDIS_URL"), fmt.Sprintf("worker-test-rl-%d", os.Getpid())))
		rateLimiters[i].(*redisRateLimiter).jitter = time.Duration(rand.Int63n(2*maxJitter.Nanoseconds()) - maxJitter.Nanoseconds())
	}

	th := tb.NewThrottler(50 * time.Millisecond)
	defer th.Close()

	var rateLimitedTimesMutex sync.Mutex
	var rateLimitedTimes []time.Time
	addTime := func(t time.Time) {
		if th.Halt("load-test", 1, 20) {
			rateLimitedTimesMutex.Lock()
			rateLimitedTimes = append(rateLimitedTimes, t)
			rateLimitedTimesMutex.Unlock()
			fmt.Print(".")
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < concurrencyCount; i++ {
		wg.Add(1)
		go func(rateLimiter RateLimiter) {
			var wg2 sync.WaitGroup
			for j := 0; j < 150; j++ {
				wg2.Add(1)
				go func() {
					for {
						ok, err := rateLimiter.RateLimit("load-test", 10, time.Second)
						if err != nil {
							t.Errorf("error in goroutine %d: %v", i, err)
							break
						}
						if ok {
							addTime(time.Now())
							break
						}
					}
					wg2.Done()
				}()
			}

			wg2.Wait()
			wg.Done()
		}(rateLimiters[i])
	}

	wg.Wait()

	if len(rateLimitedTimes) != 0 {
		t.Errorf("expected 0 rate limits, got %d", len(rateLimitedTimes))
	}
}
