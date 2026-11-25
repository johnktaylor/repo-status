package main

import "sync"

// runBoundedConcurrently invokes work for every index from zero to count-1 using a fixed pool
// of at most maxConcurrentExec workers. It blocks until all work finishes, so callers know every
// index has been processed; work items must coordinate any shared state themselves.
func runBoundedConcurrently(count int, work func(int)) {
	if count <= 0 {
		return
	}

	workerCount := count
	if workerCount > maxConcurrentExec {
		workerCount = maxConcurrentExec
	}

	indexes := make(chan int)
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for worker := 0; worker < workerCount; worker++ {
		go func() {
			defer workers.Done()
			for index := range indexes {
				work(index)
			}
		}()
	}

	for index := 0; index < count; index++ {
		indexes <- index
	}
	close(indexes)
	workers.Wait()
}
