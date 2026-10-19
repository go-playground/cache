package benchmarks

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var benchLine = regexp.MustCompile(`^Benchmark(\S+?)(?:-(\d+))?\s+\d+\s+([\d.]+) ns/op`)

// TestReport turns `go test -bench` output into tables grouped by algorithm, one row per implementation and one
// column per core count, using the median of repeated runs. Set REPORT to the output file to run it.
//
//	REPORT=bench.txt go test -run=TestReport -v
func TestReport(t *testing.T) {
	path := os.Getenv("REPORT")
	if path == "" {
		t.Skip("set REPORT to a go test -bench output file")
	}
	results, cores, err := parseBench(path)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var sb strings.Builder
	for _, workload := range workloads(results) {
		fmt.Fprintf(&sb, "\n### %s\n", workload)
		for _, alg := range []string{AlgLRU, AlgLFU, AlgTinyLFU, AlgS3FIFO, AlgSIEVE, AlgMap} {
			writeGroup(&sb, results, cores, workload, alg)
		}
	}
	t.Log(sb.String())
}

type benchKey struct{ workload, impl, cores string }

func parseBench(path string) (map[benchKey][]float64, []string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open: %w", err)
	}
	defer func() { _ = f.Close() }()
	results := map[benchKey][]float64{}
	coreSet := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		m := benchLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		cores := m[2]
		if cores == "" {
			cores = "1"
		}
		i := strings.LastIndex(m[1], "/")
		ns, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			return nil, nil, fmt.Errorf("parse ns/op: %w", err)
		}
		k := benchKey{workload: m[1][:i], impl: m[1][i+1:], cores: cores}
		results[k] = append(results[k], ns)
		coreSet[cores] = true
	}
	var cores []string
	for c := range coreSet {
		cores = append(cores, c)
	}
	sort.Slice(cores, func(i, j int) bool { a, _ := strconv.Atoi(cores[i]); b, _ := strconv.Atoi(cores[j]); return a < b })
	return results, cores, sc.Err()
}

func workloads(results map[benchKey][]float64) []string {
	var out []string
	for k := range results {
		if !slices.Contains(out, k.workload) {
			out = append(out, k.workload)
		}
	}
	sort.Strings(out)
	return out
}

func writeGroup(sb *strings.Builder, results map[benchKey][]float64, cores []string, workload, alg string) {
	var impls []string
	for _, impl := range Impls() {
		if impl.Algorithm == alg {
			impls = append(impls, impl.Name)
		}
	}
	fmt.Fprintf(sb, "\n%s (ns/op, lower is better)\n\n| cache |", alg)
	for _, c := range cores {
		fmt.Fprintf(sb, " %s core |", c)
	}
	fmt.Fprintf(sb, "\n|---|%s\n", strings.Repeat("---|", len(cores)))
	for _, impl := range impls {
		fmt.Fprintf(sb, "| %s |", impl)
		for _, c := range cores {
			v := results[benchKey{workload, impl, c}]
			if len(v) == 0 {
				sb.WriteString(" - |")
				continue
			}
			slices.Sort(v)
			fmt.Fprintf(sb, " %.1f |", v[len(v)/2])
		}
		sb.WriteString("\n")
	}
}
