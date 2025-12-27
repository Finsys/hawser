package metrics

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Finsys/hawser/internal/docker"
	"github.com/Finsys/hawser/internal/protocol"
)

// Collector gathers host metrics
type Collector struct {
	dockerClient *docker.Client
	mu           sync.Mutex // Protects prevCPU and prevTime
	prevCPU      *cpuStats
	prevTime     time.Time
}

type cpuStats struct {
	user   uint64
	nice   uint64
	system uint64
	idle   uint64
	iowait uint64
}

// NewCollector creates a new metrics collector
func NewCollector(dockerClient *docker.Client) *Collector {
	return &Collector{
		dockerClient: dockerClient,
	}
}

// Collect gathers all host metrics
func (c *Collector) Collect() (*protocol.HostMetrics, error) {
	metrics := &protocol.HostMetrics{
		CPUCores: runtime.NumCPU(),
	}

	// Collect CPU usage
	cpuUsage, err := c.collectCPU()
	if err == nil {
		metrics.CPUUsage = cpuUsage
	}

	// Collect memory
	memTotal, memUsed, memFree, err := c.collectMemory()
	if err == nil {
		metrics.MemoryTotal = memTotal
		metrics.MemoryUsed = memUsed
		metrics.MemoryFree = memFree
	}

	// Collect disk usage for Docker data root
	diskTotal, diskUsed, diskFree, err := c.collectDisk()
	if err == nil {
		metrics.DiskTotal = diskTotal
		metrics.DiskUsed = diskUsed
		metrics.DiskFree = diskFree
	}

	// Collect network stats
	rxBytes, txBytes, err := c.collectNetwork()
	if err == nil {
		metrics.NetworkRxBytes = rxBytes
		metrics.NetworkTxBytes = txBytes
	}

	// Collect host uptime
	uptime, err := c.collectUptime()
	if err == nil {
		metrics.Uptime = uptime
	}

	return metrics, nil
}

// collectCPU calculates CPU usage percentage
func (c *Collector) collectCPU() (float64, error) {
	if runtime.GOOS != "linux" {
		return 0, nil // Only Linux supported for now
	}

	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) < 5 {
				continue
			}

			user, _ := strconv.ParseUint(fields[1], 10, 64)
			nice, _ := strconv.ParseUint(fields[2], 10, 64)
			system, _ := strconv.ParseUint(fields[3], 10, 64)
			idle, _ := strconv.ParseUint(fields[4], 10, 64)
			var iowait uint64
			if len(fields) > 5 {
				iowait, _ = strconv.ParseUint(fields[5], 10, 64)
			}

			current := &cpuStats{
				user:   user,
				nice:   nice,
				system: system,
				idle:   idle,
				iowait: iowait,
			}

			// Lock mutex for thread-safe access to prevCPU and prevTime
			c.mu.Lock()
			defer c.mu.Unlock()

			if c.prevCPU == nil {
				c.prevCPU = current
				c.prevTime = time.Now()
				return 0, nil
			}

			// Calculate deltas
			userDelta := current.user - c.prevCPU.user
			niceDelta := current.nice - c.prevCPU.nice
			systemDelta := current.system - c.prevCPU.system
			idleDelta := current.idle - c.prevCPU.idle
			iowaitDelta := current.iowait - c.prevCPU.iowait

			total := userDelta + niceDelta + systemDelta + idleDelta + iowaitDelta
			if total == 0 {
				return 0, nil
			}

			usage := float64(userDelta+niceDelta+systemDelta) / float64(total) * 100

			c.prevCPU = current
			c.prevTime = time.Now()

			return usage, nil
		}
	}

	return 0, nil
}

// collectMemory reads memory statistics
func (c *Collector) collectMemory() (total, used, free uint64, err error) {
	if runtime.GOOS != "linux" {
		// Use syscall for other platforms
		return c.collectMemorySyscall()
	}

	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, err
	}
	defer file.Close()

	var memTotal, memFree, memAvailable, buffers, cached uint64

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		value, _ := strconv.ParseUint(fields[1], 10, 64)
		value *= 1024 // Convert from KB to bytes

		switch fields[0] {
		case "MemTotal:":
			memTotal = value
		case "MemFree:":
			memFree = value
		case "MemAvailable:":
			memAvailable = value
		case "Buffers:":
			buffers = value
		case "Cached:":
			cached = value
		}
	}

	// If MemAvailable is not present (older kernels), calculate it
	if memAvailable == 0 {
		memAvailable = memFree + buffers + cached
	}

	return memTotal, memTotal - memAvailable, memAvailable, nil
}

// collectMemorySyscall uses syscall for memory on non-Linux systems
func (c *Collector) collectMemorySyscall() (total, used, free uint64, err error) {
	// Non-Linux systems: metrics not available via this method
	// The agent is primarily designed to run on Linux (in Docker)
	// Return zeros - Dockhand will show "N/A" for these metrics
	return 0, 0, 0, nil
}

// collectDisk reads disk usage for Docker data root
func (c *Collector) collectDisk() (total, used, free uint64, err error) {
	// Get Docker data root
	dataRoot, err := c.dockerClient.GetDataRoot(context.Background())
	if err != nil {
		dataRoot = "/var/lib/docker"
	}

	var stat syscall.Statfs_t
	if err := syscall.Statfs(dataRoot, &stat); err != nil {
		return 0, 0, 0, err
	}

	total = stat.Blocks * uint64(stat.Bsize)
	free = stat.Bavail * uint64(stat.Bsize)
	used = total - free

	return total, used, free, nil
}

// collectNetwork reads network interface statistics
func (c *Collector) collectNetwork() (rxBytes, txBytes uint64, err error) {
	if runtime.GOOS != "linux" {
		return 0, 0, nil
	}

	file, err := os.Open("/proc/net/dev")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		if lineNum <= 2 {
			continue // Skip header lines
		}

		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}

		// Skip loopback
		iface := strings.TrimSuffix(fields[0], ":")
		if iface == "lo" {
			continue
		}

		rx, _ := strconv.ParseUint(fields[1], 10, 64)
		tx, _ := strconv.ParseUint(fields[9], 10, 64)

		rxBytes += rx
		txBytes += tx
	}

	return rxBytes, txBytes, nil
}

// collectUptime reads host uptime from /proc/uptime
func (c *Collector) collectUptime() (uint64, error) {
	if runtime.GOOS != "linux" {
		return 0, nil
	}

	file, err := os.Open("/proc/uptime")
	if err != nil {
		return 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) >= 1 {
			// First field is uptime in seconds (with decimals)
			uptime, err := strconv.ParseFloat(fields[0], 64)
			if err == nil {
				return uint64(uptime), nil
			}
		}
	}

	return 0, nil
}
