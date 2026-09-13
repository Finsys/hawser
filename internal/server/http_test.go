package server

import (
	"path/filepath"
	"testing"
)

// TestAddDiskInfo_Success verifies that a valid, statable path adds
// diskTotal/diskUsed/diskFree to the info map with the same values
// DiskUsage() reports, so /_hawser/info exposes real numbers on success.
func TestAddDiskInfo_Success(t *testing.T) {
	info := map[string]interface{}{"mode": "standard"}

	addDiskInfo(info, t.TempDir())

	total, ok := info["diskTotal"].(uint64)
	if !ok || total == 0 {
		t.Fatalf("info[diskTotal] = %v (ok=%v), want a non-zero uint64", info["diskTotal"], ok)
	}
	used, ok := info["diskUsed"].(uint64)
	if !ok {
		t.Fatalf("info[diskUsed] = %v (ok=%v), want a uint64", info["diskUsed"], ok)
	}
	free, ok := info["diskFree"].(uint64)
	if !ok {
		t.Fatalf("info[diskFree] = %v (ok=%v), want a uint64", info["diskFree"], ok)
	}
	if used+free != total {
		t.Fatalf("used(%d) + free(%d) != total(%d)", used, free, total)
	}

	// Unrelated keys must survive untouched.
	if info["mode"] != "standard" {
		t.Fatalf("info[mode] = %v, want unchanged \"standard\"", info["mode"])
	}
}

// TestAddDiskInfo_Error verifies that a path invisible to statfs leaves the
// info map without disk keys, instead of writing a misleading 0 -- mirroring
// the nil-pointer convention protocol.HostMetrics uses on the Edge-mode
// wire format (see collector_test.go in internal/metrics).
func TestAddDiskInfo_Error(t *testing.T) {
	info := map[string]interface{}{"mode": "standard"}
	missing := filepath.Join(t.TempDir(), "does-not-exist", "nested", "path")

	addDiskInfo(info, missing)

	for _, key := range []string{"diskTotal", "diskUsed", "diskFree"} {
		if _, present := info[key]; present {
			t.Errorf("info[%s] = %v, want key absent after a stat failure", key, info[key])
		}
	}
	if info["mode"] != "standard" {
		t.Fatalf("info[mode] = %v, want unchanged \"standard\"", info["mode"])
	}
}
