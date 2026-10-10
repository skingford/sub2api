package requesttrace

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type benchmarkTraceWriter struct {
	io.Writer
	delay  time.Duration
	writes atomic.Int64
}

func (w *benchmarkTraceWriter) Write(p []byte) (int, error) {
	if w.delay > 0 {
		time.Sleep(w.delay)
	}
	w.writes.Add(1)
	return w.Writer.Write(p)
}
func (*benchmarkTraceWriter) Close() error { return nil }

// Fixed request/chunk counts permit before/after comparisons with the same
// payloads. first_chunk and chunk_gap measure recorder overhead, not model TTFT.
func BenchmarkRequestTraceStreaming(b *testing.B) {
	for _, mode := range []string{"discard", "file", "slow_1ms"} {
		for _, size := range []int{1024, 32768} {
			for _, workers := range []int{1, 16} {
				b.Run(fmt.Sprintf("%s/%dB/%dworkers", mode, size, workers), func(b *testing.B) {
					w := &benchmarkTraceWriter{Writer: io.Discard}
					if mode == "file" {
						f, err := os.Create(filepath.Join(b.TempDir(), "trace.jsonl"))
						if err != nil {
							b.Fatal(err)
						}
						b.Cleanup(func() { _ = f.Close() })
						w.Writer = f
					}
					if mode == "slow_1ms" {
						w.delay = time.Millisecond
					}
					r := &Recorder{writer: w, instance: "local-benchmark"}
					payload := bytes.Repeat([]byte("x"), size)
					first := make([]time.Duration, b.N)
					gaps := make([]time.Duration, b.N*7)
					var next atomic.Int64
					var wg sync.WaitGroup
					b.ReportAllocs()
					b.SetBytes(int64(size * 8))
					b.ResetTimer()
					for range workers {
						wg.Add(1)
						go func() {
							defer wg.Done()
							for {
								i := int(next.Add(1)) - 1
								if i >= b.N {
									return
								}
								_, trace := r.Start(context.Background())
								s := trace.Stream("response", 1, int64(size*8))
								for chunk := range 8 {
									started := time.Now()
									s.Write(payload)
									elapsed := time.Since(started)
									if chunk == 0 {
										first[i] = elapsed
									} else {
										gaps[i*7+chunk-1] = elapsed
									}
								}
								s.Finish("eof", nil)
							}
						}()
					}
					wg.Wait()
					b.StopTimer()
					if got := w.writes.Load(); got != int64(b.N*9) {
						b.Fatalf("lost events: %d", got)
					}
					stats := r.Stats()
					if stats.Failures != 0 {
						b.Fatalf("trace failures: %d", stats.Failures)
					}
					for label, value := range map[string]time.Duration{"encode": stats.EncodeTime, "lock_wait": stats.LockWait, "write": stats.WriteTime} {
						b.ReportMetric(float64(value.Nanoseconds())/float64(stats.Events)/1000, label+"_us/event")
					}
					for label, values := range map[string][]time.Duration{"first_chunk": first, "chunk_gap": gaps} {
						sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
						b.ReportMetric(float64(values[(len(values)-1)*95/100].Nanoseconds())/1000, label+"_p95_us")
					}
				})
			}
		}
	}
}
