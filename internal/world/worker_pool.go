package world

import (
	"runtime"
	"sync"
)

// inboundJob 业务池任务：按连接串行 drain，全局 worker 抢执行。
type inboundJob func()

// WorkerPool 有界协程池：固定 worker 数，任务队列满时由调用方决定丢弃/阻塞。
type WorkerPool struct {
	jobs chan inboundJob
	wg   sync.WaitGroup
}

// NewWorkerPool 创建并启动 workers 个消费者；jobs 缓冲为 queueSize。
func NewWorkerPool(workers, queueSize int) *WorkerPool {
	if workers <= 0 {
		workers = runtime.NumCPU() * 4
		if workers < 8 {
			workers = 8
		}
	}
	if queueSize <= 0 {
		queueSize = 4096
	}
	p := &WorkerPool{jobs: make(chan inboundJob, queueSize)}
	p.wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer p.wg.Done()
			for job := range p.jobs {
				if job != nil {
					job()
				}
			}
		}()
	}
	return p
}

// TrySubmit 非阻塞投递；队列满返回 false。
func (p *WorkerPool) TrySubmit(job inboundJob) bool {
	if p == nil || job == nil {
		return false
	}
	select {
	case p.jobs <- job:
		return true
	default:
		return false
	}
}

// Stop 关闭任务队列并等待 worker 退出（进程退出时可选调用）。
func (p *WorkerPool) Stop() {
	if p == nil {
		return
	}
	close(p.jobs)
	p.wg.Wait()
}
