package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHostMetrics_DiskFieldsOmittedWhenNil verifies that nil Disk* pointers
// (set by the collector when a disk stat fails) are omitted from the JSON
// wire format entirely, rather than serialized as 0. This is the fix for
// the bug where a failed statfs was indistinguishable from a legitimately
// empty disk on the Dockhand side.
func TestHostMetrics_DiskFieldsOmittedWhenNil(t *testing.T) {
	m := HostMetrics{
		CPUCores:    4,
		MemoryTotal: 1000,
		// DiskTotal, DiskUsed, DiskFree intentionally left nil.
		Uptime: 42,
	}

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	for _, key := range []string{`"diskTotal"`, `"diskUsed"`, `"diskFree"`} {
		if strings.Contains(string(out), key) {
			t.Errorf("Marshal() output contains %s, want it omitted when nil: %s", key, out)
		}
	}

	// Non-disk fields must still be present -- only the disk fields are
	// pointer/omitempty.
	for _, key := range []string{`"cpuCores"`, `"memoryTotal"`, `"uptime"`} {
		if !strings.Contains(string(out), key) {
			t.Errorf("Marshal() output missing %s: %s", key, out)
		}
	}

	// Round-trip: unmarshaling must leave the pointers nil, not a pointer
	// to zero.
	var decoded HostMetrics
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded.DiskTotal != nil || decoded.DiskUsed != nil || decoded.DiskFree != nil {
		t.Errorf("Unmarshal() Disk* = (%v, %v, %v), want all nil", decoded.DiskTotal, decoded.DiskUsed, decoded.DiskFree)
	}
}

// TestHostMetrics_DiskFieldsPresentWhenSet verifies that a legitimate zero
// value (e.g. DiskFree == 0 on a full disk) is still sent on the wire as an
// actual "0", not omitted -- distinguishing it from "no data available".
func TestHostMetrics_DiskFieldsPresentWhenSet(t *testing.T) {
	total := uint64(1000)
	used := uint64(1000)
	free := uint64(0) // legitimate zero: disk is full

	m := HostMetrics{
		DiskTotal: &total,
		DiskUsed:  &used,
		DiskFree:  &free,
	}

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	if !strings.Contains(string(out), `"diskFree":0`) {
		t.Errorf("Marshal() output = %s, want \"diskFree\":0 present (legitimate zero, not omitted)", out)
	}

	var decoded HostMetrics
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}
	if decoded.DiskTotal == nil || *decoded.DiskTotal != total {
		t.Errorf("DiskTotal = %v, want pointer to %d", decoded.DiskTotal, total)
	}
	if decoded.DiskFree == nil || *decoded.DiskFree != 0 {
		t.Errorf("DiskFree = %v, want non-nil pointer to 0", decoded.DiskFree)
	}
}

// TestHostMetrics_LegacyConsumerCompat verifies that a consumer decoding
// into the pre-fix, plain-uint64 shape (no pointers) still succeeds and
// gets 0 for an omitted disk field -- i.e. the wire change is additive and
// does not break a reader that has not been updated yet.
func TestHostMetrics_LegacyConsumerCompat(t *testing.T) {
	type legacyHostMetrics struct {
		CPUCores  int    `json:"cpuCores"`
		DiskTotal uint64 `json:"diskTotal"`
		DiskUsed  uint64 `json:"diskUsed"`
		DiskFree  uint64 `json:"diskFree"`
	}

	m := HostMetrics{CPUCores: 2} // Disk* nil -> omitted

	out, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("Marshal() error: %v", err)
	}

	var legacy legacyHostMetrics
	if err := json.Unmarshal(out, &legacy); err != nil {
		t.Fatalf("legacy Unmarshal() error: %v", err)
	}
	if legacy.CPUCores != 2 {
		t.Errorf("legacy.CPUCores = %d, want 2", legacy.CPUCores)
	}
	if legacy.DiskTotal != 0 || legacy.DiskUsed != 0 || legacy.DiskFree != 0 {
		t.Errorf("legacy Disk* = (%d, %d, %d), want all 0 (Go zero value for an absent field)",
			legacy.DiskTotal, legacy.DiskUsed, legacy.DiskFree)
	}
}
