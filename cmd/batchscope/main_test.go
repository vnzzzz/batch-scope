package main

import (
	"strings"
	"testing"
)

func TestDefaultListenAddress(t *testing.T) {
	t.Setenv("PORT", "18080")

	got, err := defaultListenAddress()
	if err != nil {
		t.Fatal(err)
	}
	if got != "0.0.0.0:18080" {
		t.Fatalf("address = %q, want %q", got, "0.0.0.0:18080")
	}
}

func TestDefaultListenAddressRejectsInvalidPort(t *testing.T) {
	t.Setenv("PORT", "not-a-port")

	_, err := defaultListenAddress()
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "invalid PORT") {
		t.Fatalf("error = %q, want invalid PORT", err)
	}
}

func TestDefaultListenAddressRejectsOutOfRangePort(t *testing.T) {
	t.Setenv("PORT", "70000")

	_, err := defaultListenAddress()
	if err == nil {
		t.Fatal("expected an error")
	}
}

func TestDefaultDataDirectory(t *testing.T) {
	t.Setenv("BATCHSCOPE_DATA_DIR", "/tmp/batchscope-test")
	if got := defaultDataDirectory(); got != "/tmp/batchscope-test" {
		t.Fatalf("data directory = %q, want /tmp/batchscope-test", got)
	}
}

func TestDefaultDataDirectoryFallback(t *testing.T) {
	t.Setenv("BATCHSCOPE_DATA_DIR", "")
	if got := defaultDataDirectory(); got != "/tmp/batchscope" {
		t.Fatalf("data directory = %q, want /tmp/batchscope", got)
	}
}
