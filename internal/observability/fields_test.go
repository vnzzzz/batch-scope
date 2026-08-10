package observability

import (
	"reflect"
	"testing"
	"time"
)

func TestAttrsUsesOnlyCommonFieldNames(t *testing.T) {
	attrs := Attrs(Fields{
		RequestID: "request", Operation: "analysis", Duration: 1500 * time.Microsecond,
		BootID: "boot", SnapshotID: "snapshot", ImportID: "import", TargetID: "target",
		ReachedNodes: 1, ReturnedTreeNodes: 2, ReturnedLimits: 3, CyclesDetected: 4,
		ErrorType: "none",
	})
	names := make([]string, len(attrs))
	for index, attr := range attrs {
		names[index] = attr.Key
	}
	want := []string{
		RequestID, Operation, DurationMS, BootID, SnapshotID, ImportID, TargetID,
		ReachedNodes, ReturnedTreeNodes, ReturnedLimits, CyclesDetected, ErrorType,
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("attribute names = %v, want %v", names, want)
	}
}
