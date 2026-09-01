package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

var spinFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner reports lookup progress on stderr, so it stays out of piped output.
// When stderr is not a terminal every method is a no-op.
type Spinner struct {
	total int
	done  atomic.Int64
	hits  atomic.Int64
	label atomic.Value // string
	stop  chan struct{}
	wg    sync.WaitGroup
}

func StartSpinner(label string, total int) *Spinner {
	s := &Spinner{total: total}
	s.label.Store(label)
	if !isTTY(os.Stderr) {
		return s
	}
	s.stop = make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for i := 0; ; i++ {
			select {
			case <-s.stop:
				fmt.Fprint(os.Stderr, "\r\033[K")
				return
			case <-t.C:
				cur, _ := s.label.Load().(string)
				if done := s.done.Load(); done < int64(s.total) {
					fmt.Fprintf(os.Stderr, "\r\033[K%s %s %d/%d  ·  %d available",
						spinFrames[i%len(spinFrames)], cur, done, s.total, s.hits.Load())
				} else {
					fmt.Fprintf(os.Stderr, "\r\033[K%s %s  ·  %d available",
						spinFrames[i%len(spinFrames)], cur, s.hits.Load())
				}
			}
		}
	}()
	return s
}

// Tick records one completed lookup.
func (s *Spinner) Tick(r Result) {
	s.done.Add(1)
	if r.Status == StatusAvailable {
		s.hits.Add(1)
	}
}

// SetLabel renames the phase, so the spinner stops claiming to be checking
// domains once it is really waiting on the price list.
func (s *Spinner) SetLabel(label string) {
	if s.stop != nil {
		s.label.Store(label)
	}
}

func (s *Spinner) Stop() {
	if s.stop == nil {
		return
	}
	close(s.stop)
	s.wg.Wait()
	s.stop = nil
}
