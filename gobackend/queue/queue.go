package queue

import "sync"

// InMemoryQueue is an MVP queue (replaceable by Redis Streams later).
// It runs tasks in background worker goroutines.
type InMemoryQueue struct {
	ch     chan func()
	once   sync.Once
	closed chan struct{}
}

func NewInMemoryQueue(buffer int) *InMemoryQueue {
	if buffer <= 0 {
		buffer = 128
	}
	return &InMemoryQueue{
		ch:     make(chan func(), buffer),
		closed: make(chan struct{}),
	}
}

func (q *InMemoryQueue) Start(workers int) {
	if workers <= 0 {
		workers = 1
	}
	q.once.Do(func() {
		for i := 0; i < workers; i++ {
			go func() {
				for {
					select {
					case fn, ok := <-q.ch:
						if !ok {
							return
						}
						if fn != nil {
							fn()
						}
					case <-q.closed:
						return
					}
				}
			}()
		}
	})
}

func (q *InMemoryQueue) Enqueue(fn func()) {
	select {
	case <-q.closed:
		return
	default:
	}
	select {
	case q.ch <- fn:
	default:
		// Drop on full (MVP). In production you'd block or persist.
	}
}

func (q *InMemoryQueue) Stop() {
	select {
	case <-q.closed:
		return
	default:
		close(q.closed)
		close(q.ch)
	}
}
