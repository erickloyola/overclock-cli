package engine

import (
	"context"
	"sync"

	"overclock/pkg/client"
	"overclock/pkg/pruner"
)

// Task represents an individual unit of work for a worker.
type Task struct {
	Index        int
	Source       string // filename, line number, or label
	Content      string // file data or line text
	Prompt       string
	SystemPrompt string
}

// TaskResult contains the output and execution metrics of a Task.
type TaskResult struct {
	Task       Task
	Result     *client.ExecutionResult
	PruneStats pruner.Stats
	Err        error
}

// Pool executes Tasks concurrently using a fixed pool of goroutines.
type Pool struct {
	concurrency int
	model       string
	runner      Runner
	pruneOpts   pruner.Options
	pruneActive bool
}

// NewPool initializes a concurrent worker pool.
func NewPool(concurrency int, model string, runner Runner, pruneActive bool, pruneOpts pruner.Options) *Pool {
	if concurrency <= 0 {
		concurrency = 4
	}
	return &Pool{
		concurrency: concurrency,
		model:       model,
		runner:      runner,
		pruneOpts:   pruneOpts,
		pruneActive: pruneActive,
	}
}

// Run processes tasks from the input channel and sends completed results to the output channel.
func (p *Pool) Run(ctx context.Context, tasks <-chan Task, results chan<- TaskResult) {
	var wg sync.WaitGroup

	for i := 0; i < p.concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()

			for {
				select {
				case <-ctx.Done():
					return
				case task, ok := <-tasks:
					if !ok {
						return
					}

					// Process task
					processedContent := task.Content
					var stats pruner.Stats

					if p.pruneActive {
						processedContent, stats = pruner.Prune(task.Content, p.pruneOpts)
					}

					reqOpts := client.RequestOptions{
						Model:        p.model,
						SystemPrompt: task.SystemPrompt,
						Prompt:       task.Prompt,
						ContextData:  processedContent,
						Stream:       false, // Batch/map mode uses non-streaming to avoid interleaving STDOUT
					}

					execRes, err := p.runner.Execute(ctx, reqOpts)

					select {
					case <-ctx.Done():
						return
					case results <- TaskResult{
						Task:       task,
						Result:     execRes,
						PruneStats: stats,
						Err:        err,
					}:
					}
				}
			}
		}(i)
	}

	// Close results channel when all workers finish
	go func() {
		wg.Wait()
		close(results)
	}()
}
