package metrics

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Finsys/hawser/internal/protocol"
)

// TestDiskUsage_Success verifies that a valid, statable path returns
// non-error disk figures with total == used + free.
func TestDiskUsage_Success(t *testing.T) {
	total, used, free, err := DiskUsage(t.TempDir())
	if err != nil {
		t.Fatalf("DiskUsage() unexpected error: %v", err)
	}
	if total == 0 {
		t.Fatalf("DiskUsage() total = 0, want > 0 for a real filesystem")
	}
	if used+free != total {
		t.Fatalf("DiskUsage() used(%d) + free(%d) != total(%d)", used, free, total)
	}
}

// TestDiskUsage_Error verifies that a path invisible to statfs (e.g. not
// mounted in the agent's mount namespace) returns an error rather than
// silently reporting zeros. This is the failure path that HostMetrics'
// pointer fields exist to distinguish from a legitimate 0.
func TestDiskUsage_Error(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist", "nested", "path")

	total, used, free, err := DiskUsage(missing)
	if err == nil {
		t.Fatalf("DiskUsage(%q) expected error, got nil (total=%d used=%d free=%d)", missing, total, used, free)
	}
	if total != 0 || used != 0 || free != 0 {
		t.Fatalf("DiskUsage() on error = (%d, %d, %d), want all zero", total, used, free)
	}
}

// TestApplyDiskMetrics_Error verifies that applyDiskMetrics -- the function
// Collect() uses to write the DiskUsage() result onto HostMetrics -- keeps
// the Disk* pointer fields nil on a collection error, so the field is
// omitted from the outgoing JSON instead of reporting a misleading 0.
func TestApplyDiskMetrics_Error(t *testing.T) {
	m := &protocol.HostMetrics{}

	applyDiskMetrics(m, 0, 0, 0, os.ErrNotExist)

	if m.DiskTotal != nil || m.DiskUsed != nil || m.DiskFree != nil {
		t.Fatalf("applyDiskMetrics() on error = (%v, %v, %v), want all nil",
			m.DiskTotal, m.DiskUsed, m.DiskFree)
	}
}

// TestApplyDiskMetrics_Success verifies that applyDiskMetrics stores the
// values -- including a legitimate 0 -- as non-nil pointers on success, so
// they marshal as a real number rather than being omitted.
func TestApplyDiskMetrics_Success(t *testing.T) {
	m := &protocol.HostMetrics{}

	applyDiskMetrics(m, 1000, 400, 0, nil) // free == 0 is a legitimate value

	if m.DiskTotal == nil || *m.DiskTotal != 1000 {
		t.Fatalf("DiskTotal = %v, want pointer to 1000", m.DiskTotal)
	}
	if m.DiskUsed == nil || *m.DiskUsed != 400 {
		t.Fatalf("DiskUsed = %v, want pointer to 400", m.DiskUsed)
	}
	if m.DiskFree == nil || *m.DiskFree != 0 {
		t.Fatalf("DiskFree = %v, want non-nil pointer to 0 (legitimate zero, not omitted)", m.DiskFree)
	}
}

// TestCollect_DiskCollectionFailurePropagates ties the pieces together at
// the Collect() level for the failure path only: with SKIP_DF_COLLECTION
// set, disk collection is skipped entirely, so Disk* must stay nil. This
// does not require a Docker socket.
func TestCollect_DiskCollectionSkipped(t *testing.T) {
	t.Setenv("SKIP_DF_COLLECTION", "1")

	c := NewCollector(nil)
	metrics, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect() unexpected error: %v", err)
	}

	if metrics.DiskTotal != nil || metrics.DiskUsed != nil || metrics.DiskFree != nil {
		t.Fatalf("Collect() Disk* = (%v, %v, %v), want all nil when disk collection is skipped",
			metrics.DiskTotal, metrics.DiskUsed, metrics.DiskFree)
	}
}
