package benchmarks

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// arcTraces loads the ARC paper's storage traces (decompressed .lis files from
// https://github.com/moka-rs/cache-trace/tree/main/arc) from the directory in ARC_TRACES, if set.
func arcTraces(t *testing.T) []hitTrace {
	dir := os.Getenv("ARC_TRACES")
	if dir == "" {
		t.Log("ARC_TRACES not set: skipping ARC traces")
		return nil
	}
	specs := []struct {
		file     string
		capacity int
	}{
		{"OLTP.lis", 1_000},
		{"P8.lis", 400_000},
		{"S3.lis", 400_000},
		{"DS1.lis", 4_000_000},
	}
	var traces []hitTrace
	for _, s := range specs {
		keys, err := readLIS(filepath.Join(dir, s.file))
		if err != nil {
			t.Fatalf("reading %s: %v", s.file, err)
		}
		traces = append(traces, hitTrace{name: strings.TrimSuffix(s.file, ".lis"), capacity: s.capacity, keys: keys})
	}
	return traces
}

// readLIS parses the ARC format: each line is "start count _ _", requesting blocks start..start+count-1.
func readLIS(path string) (keys []int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open trace: %w", err)
	}
	defer func() {
		if cerr := f.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("close trace: %w", cerr)
		}
	}()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		start, err := strconv.Atoi(fields[0])
		if err != nil {
			return nil, fmt.Errorf("parse start block: %w", err)
		}
		count, err := strconv.Atoi(fields[1])
		if err != nil {
			return nil, fmt.Errorf("parse block count: %w", err)
		}
		for i := range count {
			keys = append(keys, start+i)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan trace: %w", err)
	}
	return keys, nil
}
