package app

import (
	"fmt"
	"testing"
	"time"
)

func TestImportRegistryKeepsOnlyLatestCompletedResources(t *testing.T) {
	registry := newImportRegistry()
	started := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	for index := 0; index < completedImportRetention+2; index++ {
		importID := fmt.Sprintf("imp_%032x", index)
		registry.accept(importID, started.Add(time.Duration(index)*time.Second))
		registry.succeed(importID, fmt.Sprintf("snapshot-%d", index), started.Add(time.Duration(index+1)*time.Second))
	}

	for index := 0; index < 2; index++ {
		if _, ok := registry.get(fmt.Sprintf("imp_%032x", index)); ok {
			t.Fatalf("completed import %d was retained past the FIFO limit", index)
		}
	}
	for index := 2; index < completedImportRetention+2; index++ {
		if _, ok := registry.get(fmt.Sprintf("imp_%032x", index)); !ok {
			t.Fatalf("completed import %d was evicted too early", index)
		}
	}
}

func TestImportRegistryKeepsProgressingResourceAlongsideCompletedHistory(t *testing.T) {
	registry := newImportRegistry()
	now := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)
	registry.accept("imp_progress", now)
	for index := 0; index < completedImportRetention+1; index++ {
		importID := fmt.Sprintf("imp_%032x", index)
		registry.accept(importID, now)
		registry.succeed(importID, "snapshot", now)
	}
	if resource, ok := registry.get("imp_progress"); !ok || resource.State != importStateAccepted {
		t.Fatalf("progressing resource = %#v, %t", resource, ok)
	}
}
