package balancer

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stathat/consistent"
)

// Balancer defines the behaviour each load‑balancing strategy must implement.
// Add/Remove let the caller manage the available targets (namespaces, endpoints, etc.).
// Get picks a target for the provided key (e.g. CR name) and returns it.
// Implementations MUST be safe for concurrent use by multiple goroutines.
//
// Returned errors should be ErrNoTargets if no destination is available, or any
// internal error the algorithm might raise.
// -----------------------------------------------------------------------------
// NOTE: For the dispatcher we only need Get(key), but Add/Remove make the
// balancing layer reusable elsewhere.
// -----------------------------------------------------------------------------

type Balancer interface {
	Add(target string)
	Remove(target string)
	Get(key string) (string, error)
}

var (
	// ErrUnknownBalancer is returned when the factory receives an unsupported name.
	ErrUnknownBalancer = errors.New("unknown balancer type")
	// ErrNoTargets is returned when Get is called but the balancer has zero targets.
	ErrNoTargets = errors.New("no targets registered in balancer")
)

// NewLoadBalancingFactory is a simple factory that hides the concrete implementations.
func NewLoadBalancingFactory(name string) (Balancer, error) {
	switch name {
	case "ringhash":
		return NewRingHashBalancer(), nil
	case "roundrobin":
		return NewRoundRobinBalancer(), nil
	case "leastconn":
		return NewLeastConnBalancer(), nil
	default:
		return nil, ErrUnknownBalancer
	}
}

// -----------------------------------------------------------------------------
// Ring‑Hash Balancer (consistent hashing) --------------------------------------
// -----------------------------------------------------------------------------

type ringHashBalancer struct {
	ring *consistent.Consistent
}

func NewRingHashBalancer() Balancer {
	return &ringHashBalancer{ring: consistent.New()}
}

func (r *ringHashBalancer) Add(t string)    { r.ring.Add(t) }
func (r *ringHashBalancer) Remove(t string) { r.ring.Remove(t) }
func (r *ringHashBalancer) Get(key string) (string, error) {
	if len(r.ring.Members()) == 0 {
		return "", ErrNoTargets
	}
	return r.ring.Get(key)
}

// -----------------------------------------------------------------------------
// Round‑Robin Balancer ---------------------------------------------------------
// -----------------------------------------------------------------------------

type roundRobinBalancer struct {
	targets []string
	idx     atomic.Uint64
	mu      sync.RWMutex
}

func NewRoundRobinBalancer() Balancer {
	return &roundRobinBalancer{}
}

func (r *roundRobinBalancer) Add(t string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.targets = append(r.targets, t)
}

func (r *roundRobinBalancer) Remove(t string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, v := range r.targets {
		if v == t {
			r.targets = append(r.targets[:i], r.targets[i+1:]...)
			break
		}
	}
}

func (r *roundRobinBalancer) Get(_ string) (string, error) {
	r.mu.RLock()
	n := len(r.targets)
	r.mu.RUnlock()
	if n == 0 {
		return "", ErrNoTargets
	}
	idx := r.idx.Add(1) - 1 // zero‑based
	r.mu.RLock()
	target := r.targets[idx%uint64(n)]
	r.mu.RUnlock()
	return target, nil
}

// -----------------------------------------------------------------------------
// Least‑Connections Balancer ---------------------------------------------------
// -----------------------------------------------------------------------------

// leastConnBalancer keeps a counter of active selections per target.
// When Get is called we pick the target with the fewest active connections.
// A random shuffle avoids always picking the first minimal value.

type leastConnBalancer struct {
	mu      sync.Mutex
	counts  map[string]int
	targets []string
	rng     *rand.Rand
}

func NewLeastConnBalancer() Balancer {
	return &leastConnBalancer{
		counts: make(map[string]int),
		rng:    rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

func (l *leastConnBalancer) Add(t string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.counts[t]; !ok {
		l.targets = append(l.targets, t)
		l.counts[t] = 0
	}
}

func (l *leastConnBalancer) Remove(t string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.counts, t)
	for i, v := range l.targets {
		if v == t {
			l.targets = append(l.targets[:i], l.targets[i+1:]...)
			break
		}
	}
}

func (l *leastConnBalancer) Get(_ string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.targets) == 0 {
		return "", ErrNoTargets
	}
	// Find the minimum count
	min := int(^uint(0) >> 1) // max int
	for _, t := range l.targets {
		if c := l.counts[t]; c < min {
			min = c
		}
	}
	// Collect all with the min count
	var cands []string
	for _, t := range l.targets {
		if l.counts[t] == min {
			cands = append(cands, t)
		}
	}
	target := cands[l.rng.Intn(len(cands))]
	l.counts[target]++ // increment active count
	return target, nil
}

// Done Decrement should be called by the caller when a request/connection is done.
// It's not part of the Balancer interface because the dispatcher only needs Get,
// but we expose it for completeness when using least‑connections.
func (l *leastConnBalancer) Done(target string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.counts[target] > 0 {
		l.counts[target]--
	}
}
