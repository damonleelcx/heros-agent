package sandbox

import (
	"os"
	"sync"
)

// warmPool amortizes isolate start (task 3.7: "warm pool to amortize container start"). It holds
// pre-created ephemeral scratch directories so a Run does not pay the mkdir/setup cost on the hot
// path. A scratch dir is single-use: it is destroyed with its isolate, and the pool refills lazily.
//
// The pool never hands the SAME scratch to two isolates — that would be the cross-run contamination
// the design forbids. Each Get returns an exclusively-owned dir; Put is not offered, because a used
// scratch is destroyed, not recycled.
type warmPool struct {
	mu   sync.Mutex
	free []string
	size int
}

func newWarmPool(size int) *warmPool {
	p := &warmPool{size: size}
	p.refill()
	return p
}

// get returns a ready scratch dir, creating one if the pool is empty. ok is false only if the OS
// refuses to create a temp dir, in which case the caller fails closed (no scratch → no isolate).
func (p *warmPool) get() (dir string, ok bool) {
	if p == nil {
		return mkScratch()
	}
	p.mu.Lock()
	if n := len(p.free); n > 0 {
		dir = p.free[n-1]
		p.free = p.free[:n-1]
		p.mu.Unlock()
		go p.topUp() // refill in the background so the next Get is warm too
		return dir, true
	}
	p.mu.Unlock()
	return mkScratch()
}

func (p *warmPool) refill() {
	for len(p.free) < p.size {
		d, ok := mkScratch()
		if !ok {
			return
		}
		p.free = append(p.free, d)
	}
}

func (p *warmPool) topUp() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.refill()
}

func mkScratch() (string, bool) {
	d, err := os.MkdirTemp("", "heros-isolate-")
	if err != nil {
		return "", false
	}
	return d, true
}
