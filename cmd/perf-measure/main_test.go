package main

import (
	"reflect"
	"testing"
)

func TestSummarizeUsesNearestRankP95(t *testing.T) {
	values := []int64{10, 1, 9, 2, 8, 3, 7, 4, 6, 5, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}
	got := summarize(values)
	want := distribution{Min: 1, Median: 10, P95: 19, Max: 20}
	if got != want {
		t.Fatalf("summarize() = %#v, want %#v", got, want)
	}
}

func TestParseConfigRequiresRepeatedRunsAndExactConcurrencies(t *testing.T) {
	configured, err := parseConfig([]string{"-runs", "2", "-concurrencies", "1,2,4,8"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(configured.Concurrencies, []int{1, 2, 4, 8}) {
		t.Fatalf("concurrencies = %v", configured.Concurrencies)
	}
	if _, err := parseConfig([]string{"-runs", "1"}); err == nil {
		t.Fatal("parseConfig() accepted one run")
	}
	if _, err := parseConfig([]string{"-concurrencies", "1x,2"}); err == nil {
		t.Fatal("parseConfig() accepted a non-integer concurrency")
	}
	if configured, err := parseConfig([]string{"-mode", "import", "-runs", "2"}); err != nil || configured.Mode != "import" {
		t.Fatalf("parseConfig(import) = %#v, %v", configured, err)
	}
}

func TestParseConfigValidatesCustomSize(t *testing.T) {
	configured, err := parseConfig([]string{
		"-profile", "custom", "-nodes", "20000", "-relations", "55000", "-runs", "2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if configured.Nodes != 20_000 || configured.Relations != 55_000 {
		t.Fatalf("custom size = %d nodes and %d relations", configured.Nodes, configured.Relations)
	}
	for _, arguments := range [][]string{
		{"-nodes", "20000", "-relations", "50000", "-runs", "2"},
		{"-profile", "custom", "-nodes", "9999", "-relations", "25000", "-runs", "2"},
		{"-profile", "custom", "-nodes", "20000", "-relations", "49999", "-runs", "2"},
		{"-profile", "custom", "-nodes", "20000", "-relations", "60001", "-runs", "2"},
	} {
		if _, err := parseConfig(arguments); err == nil {
			t.Fatalf("parseConfig(%v) accepted an invalid custom size", arguments)
		}
	}
}
