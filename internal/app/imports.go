package app

import (
	"sync"
	"time"

	"batchscope/internal/importer"
)

const completedImportRetention = 16

type importState string

const (
	importStateAccepted   importState = "accepted"
	importStateValidating importState = "validating"
	importStateBuilding   importState = "building"
	importStateActivating importState = "activating"
	importStateSucceeded  importState = "succeeded"
	importStateFailed     importState = "failed"
)

type importFailure struct {
	Type    string `json:"type"`
	Status  int    `json:"status"`
	Detail  string `json:"detail"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Pointer string `json:"pointer,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

type importResource struct {
	ImportID   string         `json:"importId"`
	State      importState    `json:"state" enum:"accepted,validating,building,activating,succeeded,failed"`
	CreatedAt  time.Time      `json:"createdAt"`
	UpdatedAt  time.Time      `json:"updatedAt"`
	SnapshotID string         `json:"snapshotId,omitempty"`
	Error      *importFailure `json:"error,omitempty"`
}

type importRegistry struct {
	mu        sync.Mutex
	resources map[string]importResource
	completed []string
}

func newImportRegistry() *importRegistry {
	return &importRegistry{resources: make(map[string]importResource)}
}

func (registry *importRegistry) accept(importID string, now time.Time) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.resources[importID] = importResource{
		ImportID: importID, State: importStateAccepted, CreatedAt: now, UpdatedAt: now,
	}
}

func (registry *importRegistry) progress(importID string, progress importer.Progress, now time.Time) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	resource, ok := registry.resources[importID]
	if !ok {
		return
	}
	resource.State = importState(progress.Stage)
	resource.UpdatedAt = now
	if progress.SnapshotID != "" {
		resource.SnapshotID = progress.SnapshotID
	}
	registry.resources[importID] = resource
}

func (registry *importRegistry) succeed(importID, snapshotID string, now time.Time) {
	registry.finish(importID, importStateSucceeded, snapshotID, nil, now)
}

func (registry *importRegistry) fail(importID, snapshotID string, problem problemDetails, now time.Time) {
	failure := &importFailure{
		Type: problem.Type, Status: problem.Status, Detail: problem.Detail, File: problem.File, Line: problem.Line,
		Pointer: problem.Pointer, Reason: problem.Reason,
	}
	registry.finish(importID, importStateFailed, snapshotID, failure, now)
}

func (registry *importRegistry) finish(
	importID string,
	state importState,
	snapshotID string,
	failure *importFailure,
	now time.Time,
) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	resource, ok := registry.resources[importID]
	if !ok {
		return
	}
	resource.State = state
	resource.UpdatedAt = now
	if snapshotID != "" {
		resource.SnapshotID = snapshotID
	}
	resource.Error = failure
	registry.resources[importID] = resource
	registry.completed = append(registry.completed, importID)
	for len(registry.completed) > completedImportRetention {
		oldest := registry.completed[0]
		registry.completed = registry.completed[1:]
		delete(registry.resources, oldest)
	}
}

func (registry *importRegistry) get(importID string) (importResource, bool) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	resource, ok := registry.resources[importID]
	if !ok {
		return importResource{}, false
	}
	if resource.Error != nil {
		failure := *resource.Error
		resource.Error = &failure
	}
	return resource, true
}
