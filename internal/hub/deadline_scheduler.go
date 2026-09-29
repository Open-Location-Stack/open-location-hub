package hub

import (
	"container/heap"
	"context"
	"sync"
	"time"
)

// deadlineScheduler holds at most one pending callback per logical timer. Updating
// a timer changes its heap entry in O(log n), avoiding stale heap growth and scans
// of every active fence membership on each observation.
type deadlineScheduler struct {
	mu    sync.Mutex
	now   func() time.Time
	wake  chan struct{}
	items deadlineHeap
	byKey map[string]*scheduledDeadline
}
type scheduledDeadline struct {
	key      string
	at       time.Time
	callback func()
	index    int
}
type deadlineHeap []*scheduledDeadline

func (h deadlineHeap) Len() int           { return len(h) }
func (h deadlineHeap) Less(i, j int) bool { return h[i].at.Before(h[j].at) }
func (h deadlineHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i]; h[i].index = i; h[j].index = j }
func (h *deadlineHeap) Push(value any) {
	item := value.(*scheduledDeadline)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *deadlineHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	item.index = -1
	return item
}
func newDeadlineScheduler(now func() time.Time) *deadlineScheduler {
	return &deadlineScheduler{now: now, wake: make(chan struct{}, 1), byKey: map[string]*scheduledDeadline{}}
}
func (s *deadlineScheduler) schedule(key string, at time.Time, callback func()) {
	s.mu.Lock()
	if old := s.byKey[key]; old != nil {
		if at.IsZero() {
			heap.Remove(&s.items, old.index)
			delete(s.byKey, key)
		} else {
			old.at = at
			old.callback = callback
			heap.Fix(&s.items, old.index)
		}
	} else if !at.IsZero() {
		item := &scheduledDeadline{key: key, at: at, callback: callback}
		heap.Push(&s.items, item)
		s.byKey[key] = item
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *deadlineScheduler) runDue() {
	for {
		s.mu.Lock()
		if len(s.items) == 0 || s.items[0].at.After(s.now()) {
			s.mu.Unlock()
			return
		}
		item := heap.Pop(&s.items).(*scheduledDeadline)
		delete(s.byKey, item.key)
		s.mu.Unlock()
		item.callback()
	}
}
func (s *deadlineScheduler) run(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		s.runDue()
		s.mu.Lock()
		var delay time.Duration
		pending := len(s.items) > 0
		if pending {
			delay = s.items[0].at.Sub(s.now())
			if delay < 0 {
				delay = 0
			}
		}
		s.mu.Unlock()
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		var tick <-chan time.Time
		if pending {
			timer.Reset(delay)
			tick = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-tick:
		}
	}
}
