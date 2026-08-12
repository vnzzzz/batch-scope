package main

import "testing"

func TestRelativeDataDirectoryFromEnvironmentIsPassedToApp(t *testing.T) {
	t.Setenv("BATCHSCOPE_DATA_DIR", "./env-data")
	if got := defaultDataDirectory(); got != "./env-data" {
		t.Fatalf("data directory = %q, want ./env-data", got)
	}
}
