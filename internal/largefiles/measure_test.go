//go:build large

package largefiles

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pandatoufu/data-masking/internal/config"
	"github.com/pandatoufu/data-masking/internal/core"
)

// measureSample 描述一次受控测量：同一组数据只做一次去程+回程。
type measureSample struct {
	name  string
	file  string
	build func(target int) []byte
	size  int
}

func measureSamples(tb testing.TB) []measureSample {
	return []measureSample{
		{"text10-baseline", "基线.txt", func(n int) []byte { return largeText(n) }, size10MiB},
		{"docx10-baseline", "基线.docx", func(n int) []byte { return sizedDocx(tb, n) }, size10MiB},
		{"pptx10-baseline", "基线.pptx", func(n int) []byte { return sizedPptx(tb, n) }, size10MiB},
		{"text50", "大.txt", func(n int) []byte { return largeText(n) }, size50MiB},
		{"docx50", "大.docx", func(n int) []byte { return sizedDocx(tb, n) }, size50MiB},
		{"pptx50", "大.pptx", func(n int) []byte { return sizedPptx(tb, n) }, size50MiB},
	}
}

type measureResult struct {
	Name            string  `json:"name"`
	InputBytes      int     `json:"input_bytes"`
	MaskedBytes     int     `json:"masked_bytes"`
	RestoredBytes   int     `json:"restored_bytes"`
	Repetitions     int     `json:"repetitions"`
	MedianMS        float64 `json:"median_ms"`
	MinMS           float64 `json:"min_ms"`
	TotalAllocBytes uint64  `json:"total_alloc_bytes_median"`
	Mallocs         uint64  `json:"mallocs_median"`
	HeapPeakBytes   uint64  `json:"sampled_go_heap_peak_bytes"`
	HeapSysBytes    uint64  `json:"go_heap_sys_bytes_after"`
	GoVersion       string  `json:"go_version"`
	GOOS            string  `json:"goos"`
	GOARCH          string  `json:"goarch"`
}

// heapSampler 以固定间隔采样 runtime.MemStats.HeapAlloc，记录 Go 堆峰值；
// 这是采样近似值，不等于 OS 进程峰值，也不等于 TotalAlloc。
type heapSampler struct {
	stop chan struct{}
	done chan struct{}
	peak atomic.Uint64
}

func startHeapSampler() *heapSampler {
	h := &heapSampler{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(h.done)
		t := time.NewTicker(2 * time.Millisecond)
		defer t.Stop()
		var ms runtime.MemStats
		for {
			select {
			case <-h.stop:
				return
			case <-t.C:
				runtime.ReadMemStats(&ms)
				for {
					cur := h.peak.Load()
					if ms.HeapAlloc <= cur || h.peak.CompareAndSwap(cur, ms.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	return h
}

func (h *heapSampler) stopAndPeak() uint64 {
	close(h.stop)
	<-h.done
	return h.peak.Load()
}

// 4.3 独立测量入口：分别记录耗时、分配次数、总分配量与采样 Go 堆峰值；
// 50MiB 允许样本与旧 10MiB 范围内共同基线都实跑去程/回程。
// 原始口径：单次运行取 DM_LARGE_REPEAT（默认 2）轮中位数；每轮前 runtime.GC()，
// 采样间隔 2ms；总分配量来自 MemStats.TotalAlloc 差值，不是峰值。
func TestLargeMeasure(t *testing.T) {
	reps := 2
	if raw := os.Getenv("DM_LARGE_REPEAT"); raw != "" {
		if n, err := fmt.Sscanf(raw, "%d", &reps); n != 1 || err != nil || reps <= 0 {
			t.Fatalf("DM_LARGE_REPEAT 非法：%q", raw)
		}
	}
	var results []measureResult
	for _, s := range measureSamples(t) {
		data := s.build(s.size)
		if len(data) != s.size {
			t.Fatalf("%s 样本长度 %d != %d", s.name, len(data), s.size)
		}
		cfg := config.Defaults()
		times := make([]float64, 0, reps)
		allocs := make([]uint64, 0, reps)
		mallocsList := make([]uint64, 0, reps)
		var heapPeak, heapSys, maskedLen, restoredLen uint64
		for i := 0; i < reps; i++ {
			runtime.GC()
			sampler := startHeapSampler()
			var before, after runtime.MemStats
			runtime.ReadMemStats(&before)
			start := time.Now()
			out, err := core.MaskFile(s.file, data, core.MaskOptions{Profile: "strict"}, cfg, nil)
			if err != nil || out == nil {
				t.Fatalf("%s 去程失败：out=%v err=%v", s.name, out, err)
			}
			restored, err := core.RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil)
			if err != nil || restored == nil {
				t.Fatalf("%s 回程失败：out=%v err=%v", s.name, restored, err)
			}
			elapsed := time.Since(start)
			peak := sampler.stopAndPeak()
			runtime.ReadMemStats(&after)
			times = append(times, float64(elapsed.Microseconds())/1000)
			allocs = append(allocs, after.TotalAlloc-before.TotalAlloc)
			mallocsList = append(mallocsList, after.Mallocs-before.Mallocs)
			if i == 0 {
				heapSys = after.HeapSys
				maskedLen = uint64(len(out.MaskedBytes))
				restoredLen = uint64(len(restored.Bytes))
			}
			if peak > heapPeak {
				heapPeak = peak
			}
		}
		res := measureResult{
			Name: s.name, InputBytes: s.size, MaskedBytes: int(maskedLen), RestoredBytes: int(restoredLen),
			Repetitions: reps, MedianMS: median(times), MinMS: minFloat(times),
			TotalAllocBytes: medianUint(allocs), Mallocs: medianUint(mallocsList), HeapPeakBytes: heapPeak, HeapSysBytes: heapSys,
			GoVersion: runtime.Version(), GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		}
		results = append(results, res)
		raw, _ := json.Marshal(res)
		t.Logf("MEASURE %s", raw)
	}
	if path := os.Getenv("DM_LARGE_REPORT"); path != "" {
		raw, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatalf("写入报告失败：%v", err)
		}
	}
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]float64(nil), v...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	return cp[len(cp)/2]
}

func medianUint(v []uint64) uint64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]uint64(nil), v...)
	for i := 0; i < len(cp); i++ {
		for j := i + 1; j < len(cp); j++ {
			if cp[j] < cp[i] {
				cp[i], cp[j] = cp[j], cp[i]
			}
		}
	}
	return cp[len(cp)/2]
}

func minFloat(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	m := v[0]
	for _, x := range v[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

// BenchmarkLarge 以 -benchtime=1x 显式运行一次，记录 ns/op、B/op、allocs/op；
// 总分配量由 B/op 推导，不能当作堆峰值。
func BenchmarkLarge(b *testing.B) {
	for _, s := range measureSamples(b) {
		data := s.build(s.size)
		cfg := config.Defaults()
		b.Run(s.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				out, err := core.MaskFile(s.file, data, core.MaskOptions{Profile: "strict"}, cfg, nil)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := core.RestoreFile(out.MaskedName, out.MaskedBytes, out.MappingBytes, cfg, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
