package stream

import (
	"container/heap"
	"time"
)

type deadlineItem[T any] struct {
	key       string
	partition int32
	at        time.Time
	value     T
	index     int
}

type deadlineHeap[T any] []*deadlineItem[T]

func (h deadlineHeap[T]) Len() int { return len(h) }
func (h deadlineHeap[T]) Less(i, j int) bool {
	if h[i].at.Equal(h[j].at) {
		return h[i].key < h[j].key
	}
	return h[i].at.Before(h[j].at)
}
func (h deadlineHeap[T]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *deadlineHeap[T]) Push(value any) {
	item := value.(*deadlineItem[T])
	item.index = len(*h)
	*h = append(*h, item)
}
func (h *deadlineHeap[T]) Pop() any {
	items := *h
	item := items[len(items)-1]
	items[len(items)-1] = nil
	*h = items[:len(items)-1]
	return item
}

type deadlineQueue[T any] struct {
	items deadlineHeap[T]
	byKey map[string]*deadlineItem[T]
}

func (q *deadlineQueue[T]) update(partition int32, key string, at time.Time, value T) {
	item := q.byKey[key]
	if at.IsZero() {
		if item != nil {
			heap.Remove(&q.items, item.index)
			delete(q.byKey, key)
		}
		return
	}
	if item != nil {
		item.at, item.value = at, value
		heap.Fix(&q.items, item.index)
		return
	}
	item = &deadlineItem[T]{key: key, partition: partition, at: at, value: value}
	q.byKey[key] = item
	heap.Push(&q.items, item)
}

func (q *deadlineQueue[T]) next() time.Time {
	if len(q.items) == 0 {
		return time.Time{}
	}
	return q.items[0].at
}

func (q *deadlineQueue[T]) pop() *deadlineItem[T] {
	item := heap.Pop(&q.items).(*deadlineItem[T])
	delete(q.byKey, item.key)
	return item
}
