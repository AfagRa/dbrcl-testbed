package main

import (
	"hash/fnv"
	"math"
	"sync"
	"time"
)

// Buckets is the cohort resolution: weights are applied in steps of 1/Buckets.
const Buckets = 10000

// Bucket maps a user id to [0, Buckets): FNV-1a 32-bit hash mod 10^4.
func Bucket(uid string) int {
	h := fnv.New32a()
	h.Write([]byte(uid))
	return int(h.Sum32() % Buckets)
}

// Threshold is round(Buckets * w). A bucket b goes to canary iff b < Threshold(w).
// Threshold is monotone in w, so cohorts are nested: a user who is in the
// canary at weight w stays in it at every larger weight.
func Threshold(w float64) int {
	return int(math.Round(Buckets * w))
}

// ToCanary reports whether a bucket is in the canary cohort at threshold t.
func ToCanary(bucket, threshold int) bool { return bucket < threshold }

// weightState is the current routing weight. Reads are on the hot path.
type weightState struct {
	mu        sync.RWMutex
	weight    float64
	threshold int
	stage     int
	reason    string
	since     time.Time
}

func (s *weightState) get() (w float64, threshold, stage int, reason string, since time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.weight, s.threshold, s.stage, s.reason, s.since
}

// set stores a new weight and returns the previous one.
func (s *weightState) set(w float64, stage int, reason string) (old float64, oldStage int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, oldStage = s.weight, s.stage
	s.weight, s.threshold, s.stage, s.reason, s.since = w, Threshold(w), stage, reason, time.Now()
	return old, oldStage
}
