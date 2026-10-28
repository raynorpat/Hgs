//go:build integration

package integration

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"testing"
)

// TestMozillaSummarizeSamples reproduces the report tables from a run's
// samples.jsonl: failed samples count as errors and never as latency samples.
// GOGS_TEST_MOZILLA_SAMPLES=<path to samples.jsonl>
func TestMozillaSummarizeSamples(t *testing.T) {
	path := os.Getenv("GOGS_TEST_MOZILLA_SAMPLES")
	if path == "" {
		t.Skip("set GOGS_TEST_MOZILLA_SAMPLES to a samples.jsonl path")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	type key struct {
		kind, name  string
		concurrency int
	}
	seconds, failed := map[key][]float64{}, map[key]int{}
	var keys []key
	in := bufio.NewScanner(f)
	in.Buffer(make([]byte, 1<<20), 16<<20)
	for in.Scan() {
		var s mozillaSample
		if err := json.Unmarshal(in.Bytes(), &s); err != nil {
			t.Fatal(err)
		}
		if s.Kind == "phase-start" || s.Seconds == 0 {
			continue
		}
		k := key{s.Kind, s.Name, s.Concurrency}
		if _, ok := seconds[k]; !ok && failed[k] == 0 {
			keys = append(keys, k)
		}
		if s.Error != "" {
			failed[k]++
			continue
		}
		seconds[k] = append(seconds[k], s.Seconds)
	}
	if err := in.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		return a.kind+"\x00"+a.name+fmt.Sprint(a.concurrency) < b.kind+"\x00"+b.name+fmt.Sprint(b.concurrency)
	})
	for _, k := range keys {
		v := append([]float64(nil), seconds[k]...)
		sort.Float64s(v)
		if len(v) == 0 {
			fmt.Printf("%s\t%s\tc=%d\tok=0\tfailed=%d\n", k.kind, k.name, k.concurrency, failed[k])
			continue
		}
		fmt.Printf("%s\t%s\tc=%d\tok=%d\tfailed=%d\tmin=%.3f\tmedian=%.3f\tmax=%.3f\n", k.kind, k.name, k.concurrency, len(v), failed[k], v[0], v[len(v)/2], v[len(v)-1])
	}
}
