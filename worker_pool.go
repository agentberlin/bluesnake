// Copyright 2025 Agentic World, LLC (Sherin Thomas)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bluesnake

import (
	"context"
	"sync"
)

// WorkerPool manages a fixed number of worker goroutines that process work items from a queue.
// This provides controlled concurrency and prevents unbounded goroutine creation.
type WorkerPool struct {
	maxWorkers int
	workQueue  chan func()
	wg         *sync.WaitGroup
	ctx        context.Context
}

// NewWorkerPool creates a new worker pool with the specified number of workers and queue size.
// Parameters:
//   - ctx: Context for cancellation
//   - maxWorkers: Number of concurrent worker goroutines
//   - queueSize: Buffer size for the work queue (blocks when full)
func NewWorkerPool(ctx context.Context, maxWorkers int, queueSize int) *WorkerPool {
	wp := &WorkerPool{
		maxWorkers: maxWorkers,
		workQueue:  make(chan func(), queueSize),
		wg:         &sync.WaitGroup{},
		ctx:        ctx,
	}

	// Start worker goroutines
	for i := 0; i < maxWorkers; i++ {
		wp.wg.Add(1)
		go wp.worker()
	}

	return wp
}

// worker is the main loop for a worker goroutine.
// It continuously pulls work items from the queue and executes them.
func (wp *WorkerPool) worker() {
	defer wp.wg.Done()

	for {
		select {
		case work, ok := <-wp.workQueue:
			if !ok {
				// Channel closed, exit worker
				return
			}
			// Execute the work item
			work()

		case <-wp.ctx.Done():
			// Context cancelled, exit worker
			return
		}
	}
}

// Submit submits a work item to the pool.
// This method BLOCKS if the work queue is full, providing backpressure.
// Returns an error if the context is cancelled.
func (wp *WorkerPool) Submit(work func()) error {
	select {
	case wp.workQueue <- work:
		// Successfully queued
		return nil

	case <-wp.ctx.Done():
		// Context cancelled
		return wp.ctx.Err()
	}
}

// Close shuts down the worker pool gracefully.
// It closes the work queue and waits for all workers to finish their current tasks.
func (wp *WorkerPool) Close() {
	close(wp.workQueue)
	wp.wg.Wait()
}
