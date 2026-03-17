package main

import (
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"sync/atomic"
)

// Resolver represents an upstream DNS resolver.
type Resolver struct {
	Addr string // "IP:port"
}

func (r Resolver) String() string {
	return r.Addr
}

type resolverStats struct {
	sent uint64
	ok   uint64
	fail uint64
}

// ResolverPool manages a set of upstream resolvers with health tracking.
type ResolverPool struct {
	mu           sync.RWMutex
	resolvers    []Resolver
	healthyCache []Resolver
	stats        map[Resolver]*resolverStats
	failStreak   map[Resolver]int
	rrIndex      uint64
}

func NewResolverPool(resolvers []Resolver) *ResolverPool {
	p := &ResolverPool{
		resolvers:    resolvers,
		healthyCache: make([]Resolver, len(resolvers)),
		stats:        make(map[Resolver]*resolverStats, len(resolvers)),
		failStreak:   make(map[Resolver]int, len(resolvers)),
	}
	copy(p.healthyCache, resolvers)
	for _, r := range resolvers {
		p.stats[r] = &resolverStats{}
	}
	return p
}

func (p *ResolverPool) rebuildHealthyCache(healthy []Resolver) {
	if len(healthy) == 0 {
		healthy = make([]Resolver, len(p.resolvers))
		copy(healthy, p.resolvers)
	}
	p.healthyCache = healthy
}

func (p *ResolverPool) GetNext() Resolver {
	p.mu.RLock()
	healthy := p.healthyCache
	p.mu.RUnlock()

	if len(healthy) == 0 {
		// Fallback: should not normally happen
		p.mu.RLock()
		defer p.mu.RUnlock()
		return p.resolvers[rand.Intn(len(p.resolvers))]
	}

	idx := atomic.AddUint64(&p.rrIndex, 1) - 1
	return healthy[idx%uint64(len(healthy))]
}

// SendQuery sends a DNS query to a resolver over UDP.
func (p *ResolverPool) SendQuery(data []byte, r Resolver) ([]byte, error) {
	return sendQueryUDP(data, r.Addr, upstreamTimeout)
}

func (p *ResolverPool) MarkSent(r Resolver) {
	p.mu.RLock()
	s := p.stats[r]
	p.mu.RUnlock()
	atomic.AddUint64(&s.sent, 1)
}

func (p *ResolverPool) MarkSuccess(r Resolver) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.stats[r]
	atomic.AddUint64(&s.ok, 1)
	p.failStreak[r] = 0

	// If this resolver was previously unhealthy (not in healthyCache),
	// re-add it so future queries can use it again.
	found := false
	for _, h := range p.healthyCache {
		if h == r {
			found = true
			break
		}
	}
	if !found {
		healthy := make([]Resolver, 0, len(p.healthyCache)+1)
		healthy = append(healthy, p.healthyCache...)
		healthy = append(healthy, r)
		p.rebuildHealthyCache(healthy)
	}
}

func (p *ResolverPool) MarkFailure(r Resolver) {
	p.mu.Lock()
	s := p.stats[r]
	atomic.AddUint64(&s.fail, 1)
	p.failStreak[r]++
	if p.failStreak[r] >= 10 {
		// Remove resolver from healthy cache; it will stay in p.resolvers
		// and can be re-added on future success or by UpdateResolvers.
		newHealthy := make([]Resolver, 0, len(p.healthyCache))
		for _, h := range p.healthyCache {
			if h != r {
				newHealthy = append(newHealthy, h)
			}
		}
		p.rebuildHealthyCache(newHealthy)
		slog.Warn("Resolver marked unhealthy (evicted from rotation)", "resolver", r, "streak", p.failStreak[r])
	}
	p.mu.Unlock()
}

// UpdateResolvers replaces the active resolver list with the given ordered
// resolvers. Stats are preserved for existing resolvers and initialized for
// new ones.
func (p *ResolverPool) UpdateResolvers(ordered []Resolver) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.resolvers = ordered

	for _, r := range ordered {
		if _, ok := p.stats[r]; !ok {
			p.stats[r] = &resolverStats{}
			p.failStreak[r] = 0
		}
	}

	healthy := make([]Resolver, len(ordered))
	copy(healthy, ordered)
	p.rebuildHealthyCache(healthy)
	slog.Info("Resolver pool updated", "count", len(ordered))
}

func (p *ResolverPool) StatsString() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var result string
	for _, r := range p.resolvers {
		s, ok := p.stats[r]
		if !ok {
			continue
		}
		result += fmt.Sprintf("  %40s sent=%-6d ok=%-6d fail=%d\n",
			r.String(),
			atomic.LoadUint64(&s.sent),
			atomic.LoadUint64(&s.ok),
			atomic.LoadUint64(&s.fail))
	}
	return result
}

// FailedResolvers returns resolvers that are currently not in the healthy cache.
// Used by background health checks to only probe previously failed resolvers.
func (p *ResolverPool) FailedResolvers() []Resolver {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.resolvers) == 0 {
		return nil
	}

	healthySet := make(map[Resolver]struct{}, len(p.healthyCache))
	for _, h := range p.healthyCache {
		healthySet[h] = struct{}{}
	}

	var failed []Resolver
	for _, r := range p.resolvers {
		if _, ok := healthySet[r]; !ok {
			failed = append(failed, r)
		}
	}
	return failed
}

// MarkHealthy resets fail streak and ensures resolver is in healthy cache.
// Used by background scanner when a previously failed resolver recovers.
func (p *ResolverPool) MarkHealthy(r Resolver) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.failStreak[r] = 0

	for _, h := range p.healthyCache {
		if h == r {
			return
		}
	}

	healthy := make([]Resolver, 0, len(p.healthyCache)+1)
	healthy = append(healthy, p.healthyCache...)
	healthy = append(healthy, r)
	p.rebuildHealthyCache(healthy)
}


