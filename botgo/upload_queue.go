package main

import (
	"container/heap"
	"sync"
)

type UploadJob struct {
	TelegramID      int64
	ChatID          int64
	StatusMessageID int
	FileID          string
	Filename        string
	FileSize        int64
	Lang            string
	IsSupporter     bool
	Priority        int
	Seq             int64
}

type UploadHeap []UploadJob

func (h UploadHeap) Len() int { return len(h) }
func (h UploadHeap) Less(i, j int) bool {
	if h[i].Priority == h[j].Priority {
		return h[i].Seq < h[j].Seq
	}
	return h[i].Priority < h[j].Priority
}
func (h UploadHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *UploadHeap) Push(x any)   { *h = append(*h, x.(UploadJob)) }
func (h *UploadHeap) Pop() any {
	old := *h
	item := old[len(old)-1]
	*h = old[:len(old)-1]
	return item
}

type UploadQueue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	values UploadHeap
}

func NewUploadQueue() *UploadQueue {
	q := &UploadQueue{}
	q.cond = sync.NewCond(&q.mu)
	heap.Init(&q.values)
	return q
}

func (q *UploadQueue) Put(job UploadJob) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	heap.Push(&q.values, job)
	size := q.values.Len()
	q.cond.Signal()
	return size
}

func (q *UploadQueue) Get() UploadJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	for q.values.Len() == 0 {
		q.cond.Wait()
	}
	return heap.Pop(&q.values).(UploadJob)
}

