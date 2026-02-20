package download

import (
	"context"
	"log/slog"
	"path/filepath"
	"sort"
	"sync"
)

// Job represents a single download job.
type Job struct {
	URL              string
	DestPath         string
	ExpectedChecksum string
	ExpectedSize     int64
}

// Result represents the result of a download job.
type Result struct {
	Job      Job
	Success  bool
	Error    error
	Download *DownloadResult
	index    int // Internal: used to maintain result order
}

// Pool manages concurrent downloads using a worker pool pattern.
type Pool struct {
	client  *Client
	workers int
	logger  *slog.Logger
}

// NewPool creates a new download pool with the specified number of worker goroutines.
func NewPool(client *Client, workers int, logger *slog.Logger) *Pool {
	if workers <= 0 {
		workers = 1
	}
	return &Pool{
		client:  client,
		workers: workers,
		logger:  logger,
	}
}

// Execute submits a batch of jobs to the pool and waits for all to complete.
// The returned results maintain the same order as the input jobs.
// If the context is cancelled, all workers stop processing immediately.
func (p *Pool) Execute(ctx context.Context, jobs []Job) []Result {
	if len(jobs) == 0 {
		return []Result{}
	}

	// Create channels for jobs and results
	jobsChan := make(chan jobWithIndex, len(jobs))
	resultsChan := make(chan Result, len(jobs))

	// Create a WaitGroup to track all workers
	var wg sync.WaitGroup

	// Launch worker goroutines
	for i := 0; i < p.workers; i++ {
		wg.Add(1)
		go p.worker(ctx, jobsChan, resultsChan, &wg)
	}

	// Send jobs to the job channel in a separate goroutine
	go func() {
		for i, job := range jobs {
			select {
			case jobsChan <- jobWithIndex{job: job, index: i}:
			case <-ctx.Done():
				// If context is cancelled, stop sending jobs
				break
			}
		}
		close(jobsChan)
	}()

	// Wait for all workers to finish
	go func() {
		wg.Wait()
		close(resultsChan)
	}()

	// Collect results
	results := make([]Result, 0, len(jobs))
	for result := range resultsChan {
		results = append(results, result)
	}

	// Sort results by their original index to maintain order
	sort.Slice(results, func(i, j int) bool {
		return results[i].index < results[j].index
	})

	return results
}

// jobWithIndex pairs a Job with its original index for ordering results.
type jobWithIndex struct {
	job   Job
	index int
}

// worker processes jobs from the jobs channel and sends results to the results channel.
func (p *Pool) worker(ctx context.Context, jobsChan <-chan jobWithIndex, resultsChan chan<- Result, wg *sync.WaitGroup) {
	defer wg.Done()

	for jobWithIdx := range jobsChan {
		select {
		case <-ctx.Done():
			// Context cancelled, send a cancelled result and return
			resultsChan <- Result{
				Job:     jobWithIdx.job,
				Success: false,
				Error:   ctx.Err(),
				index:   jobWithIdx.index,
			}
			return
		default:
		}

		// Create download options from the job
		opts := DownloadOptions{
			URL:              jobWithIdx.job.URL,
			DestPath:         jobWithIdx.job.DestPath,
			ExpectedChecksum: jobWithIdx.job.ExpectedChecksum,
			ExpectedSize:     jobWithIdx.job.ExpectedSize,
			RetryCount:       3,
		}

		// Execute the download
		downloadResult, err := p.client.Download(ctx, opts)

		result := Result{
			Job:      jobWithIdx.job,
			index:    jobWithIdx.index,
			Download: downloadResult,
		}

		if err != nil {
			result.Success = false
			result.Error = err
			p.logger.Error("download job failed", "url", jobWithIdx.job.URL, "dest", filepath.Base(jobWithIdx.job.DestPath), "error", err)
		} else {
			result.Success = true
			p.logger.Info("download job completed", "url", jobWithIdx.job.URL, "dest", filepath.Base(jobWithIdx.job.DestPath), "size", downloadResult.Size)
		}

		resultsChan <- result
	}
}
