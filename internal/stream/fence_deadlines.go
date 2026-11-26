package stream

import (
	"container/heap"
	"time"
)

type fenceDeadline struct {
	key        string
	partition  int32
	at         time.Time
	membership *fenceMembership
	index      int
}

type fenceDeadlineHeap []*fenceDeadline

func (h fenceDeadlineHeap) Len() int { return len(h) }
func (h fenceDeadlineHeap) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].key < h[j].key
	}
	return h[i].at.Before(h[j].at)
}
func (h fenceDeadlineHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *fenceDeadlineHeap) Push(value any) {
	item := value.(*fenceDeadline)
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *fenceDeadlineHeap) Pop() any {
	items := *h
	item := items[len(items)-1]
	items[len(items)-1] = nil
	*h = items[:len(items)-1]
	return item
}

type fenceDeadlineQueue struct {
	items fenceDeadlineHeap
	byKey map[string]*fenceDeadline
}

func (q *fenceDeadlineQueue) update(partition int32, key string, state *fenceMembership) {
	item := q.byKey[key]
	var at time.Time
	if state != nil {
		at = state.deadlines.Next()
	}
	if at.IsZero() {
		if item != nil {
			heap.Remove(&q.items, item.index)
			delete(q.byKey, key)
		}
		return
	}
	if item != nil {
		item.at, item.membership = at, state
		heap.Fix(&q.items, item.index)
		return
	}
	item = &fenceDeadline{key: key, partition: partition, at: at, membership: state}
	q.byKey[key] = item
	heap.Push(&q.items, item)
}

func (q *fenceDeadlineQueue) next() time.Time {
	if len(q.items) == 0 {
		return time.Time{}
	}
	return q.items[0].at
}

func (q *fenceDeadlineQueue) pop() *fenceDeadline {
	item := heap.Pop(&q.items).(*fenceDeadline)
	delete(q.byKey, item.key)
	return item
}
