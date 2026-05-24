package metrics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const defaultProcRoot = "/proc"

type cpuSample struct {
	idle  uint64
	total uint64
}

type netSample map[string]NetworkStat

type mountInfo struct {
	device     string
	mountpoint string
	fsType     string
}

func collectLinux(ctx context.Context, opts Options) (Snapshot, error) {
	cpuBefore, err := readCPUSample()
	if err != nil {
		return Snapshot{}, err
	}
	netBefore, err := readNetworkStats()
	if err != nil {
		return Snapshot{}, err
	}

	timer := time.NewTimer(opts.SampleWindow)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-timer.C:
	}

	cpuAfter, err := readCPUSample()
	if err != nil {
		return Snapshot{}, err
	}
	netAfter, err := readNetworkStats()
	if err != nil {
		return Snapshot{}, err
	}

	host, err := readHostInfo()
	if err != nil {
		return Snapshot{}, err
	}
	memory, err := readMemoryStats()
	if err != nil {
		return Snapshot{}, err
	}
	filesystems, err := readFilesystemStats(opts.Root)
	if err != nil {
		return Snapshot{}, err
	}

	return Snapshot{
		SchemaVersion: "rumpty.agent.metrics.v1",
		CollectedAt:   time.Now().UTC(),
		Host:          host,
		CPU: CPUStats{
			OnlineCores: runtime.NumCPU(),
			UsagePct:    cpuUsagePct(cpuBefore, cpuAfter),
		},
		Memory:      memory,
		Filesystems: filesystems,
		Network:     networkRates(netBefore, netAfter, opts.SampleWindow),
	}, nil
}

func readHostInfo() (HostInfo, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return HostInfo{}, err
	}

	info := HostInfo{Hostname: hostname}
	if b, err := os.ReadFile(procPath("sys/kernel/random/boot_id")); err == nil {
		info.BootID = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(procPath("uptime")); err == nil {
		fields := strings.Fields(string(b))
		if len(fields) > 0 {
			info.Uptime, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	return info, nil
}

func readCPUSample() (cpuSample, error) {
	file, err := os.Open(procPath("stat"))
	if err != nil {
		return cpuSample{}, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		return cpuSample{}, errors.New("missing aggregate cpu line in /proc/stat")
	}
	fields := strings.Fields(scanner.Text())
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuSample{}, fmt.Errorf("unexpected /proc/stat cpu line: %q", scanner.Text())
	}

	var values []uint64
	for _, field := range fields[1:] {
		value, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuSample{}, fmt.Errorf("parse cpu counter %q: %w", field, err)
		}
		values = append(values, value)
	}

	var total uint64
	for _, value := range values {
		total += value
	}

	idle := values[3]
	if len(values) > 4 {
		idle += values[4]
	}

	return cpuSample{idle: idle, total: total}, scanner.Err()
}

func cpuUsagePct(before, after cpuSample) float64 {
	totalDelta := after.total - before.total
	idleDelta := after.idle - before.idle
	if totalDelta == 0 || idleDelta > totalDelta {
		return 0
	}
	return roundPct(100 * float64(totalDelta-idleDelta) / float64(totalDelta))
}

func readMemoryStats() (MemoryStats, error) {
	file, err := os.Open(procPath("meminfo"))
	if err != nil {
		return MemoryStats{}, err
	}
	defer file.Close()

	values := map[string]uint64{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		key := strings.TrimSuffix(fields[0], ":")
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return MemoryStats{}, fmt.Errorf("parse meminfo %s: %w", key, err)
		}
		values[key] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return MemoryStats{}, err
	}

	total := values["MemTotal"]
	available := values["MemAvailable"]
	if total == 0 {
		return MemoryStats{}, errors.New("MemTotal missing from /proc/meminfo")
	}
	if available > total {
		available = total
	}

	used := total - available
	return MemoryStats{
		TotalBytes:     total,
		AvailableBytes: available,
		UsedBytes:      used,
		UsedPct:        roundPct(100 * float64(used) / float64(total)),
	}, nil
}

func readFilesystemStats(root string) ([]FilesystemStat, error) {
	root = filepath.Clean(root)
	mounts, err := readMountInfo()
	if err != nil {
		return nil, err
	}

	var stats []FilesystemStat
	seen := map[string]struct{}{}
	for _, mount := range mounts {
		if !includeFilesystem(mount) {
			continue
		}
		if !mountUnderRoot(mount.mountpoint, root) {
			continue
		}
		if _, ok := seen[mount.mountpoint]; ok {
			continue
		}
		seen[mount.mountpoint] = struct{}{}

		var stat syscall.Statfs_t
		if err := syscall.Statfs(mount.mountpoint, &stat); err != nil {
			continue
		}

		total := stat.Blocks * uint64(stat.Bsize)
		available := stat.Bavail * uint64(stat.Bsize)
		free := stat.Bfree * uint64(stat.Bsize)
		if total == 0 || free > total {
			continue
		}

		used := total - free
		stats = append(stats, FilesystemStat{
			Mountpoint:     mount.mountpoint,
			FilesystemType: mount.fsType,
			Device:         mount.device,
			TotalBytes:     total,
			AvailableBytes: available,
			UsedBytes:      used,
			UsedPct:        roundPct(100 * float64(used) / float64(total)),
		})
	}
	return stats, nil
}

func mountUnderRoot(mountpoint, root string) bool {
	mountpoint = filepath.Clean(mountpoint)
	root = filepath.Clean(root)
	if root == "/" {
		return true
	}
	return mountpoint == root || strings.HasPrefix(mountpoint, root+string(os.PathSeparator))
}

func readMountInfo() ([]mountInfo, error) {
	file, err := os.Open(procPath("self/mountinfo"))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var mounts []mountInfo
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if separator < 0 || separator+2 >= len(fields) || len(fields) < 5 {
			continue
		}
		mounts = append(mounts, mountInfo{
			mountpoint: unescapeMountField(fields[4]),
			fsType:     fields[separator+1],
			device:     fields[separator+2],
		})
	}
	return mounts, scanner.Err()
}

func includeFilesystem(mount mountInfo) bool {
	switch mount.fsType {
	case "autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devpts", "devtmpfs", "efivarfs", "fusectl", "hugetlbfs", "mqueue", "nsfs", "overlay", "proc", "pstore", "ramfs", "securityfs", "sysfs", "tmpfs", "tracefs":
		return false
	default:
		return strings.HasPrefix(mount.mountpoint, "/")
	}
}

func readNetworkStats() (netSample, error) {
	file, err := os.Open(procPath("net/dev"))
	if err != nil {
		return nil, err
	}
	defer file.Close()

	active := map[string]struct{}{}
	ifaces, err := net.Interfaces()
	if err == nil {
		for _, iface := range ifaces {
			if iface.Flags&net.FlagUp != 0 && iface.Flags&net.FlagLoopback == 0 {
				active[iface.Name] = struct{}{}
			}
		}
	}

	stats := netSample{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		name := strings.TrimSpace(parts[0])
		if name == "lo" {
			continue
		}
		if len(active) > 0 {
			if _, ok := active[name]; !ok {
				continue
			}
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			continue
		}
		rx, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse rx bytes for %s: %w", name, err)
		}
		tx, err := strconv.ParseUint(fields[8], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse tx bytes for %s: %w", name, err)
		}
		stats[name] = NetworkStat{Interface: name, RxBytes: rx, TxBytes: tx}
	}
	return stats, scanner.Err()
}

func networkRates(before, after netSample, window time.Duration) []NetworkStat {
	seconds := window.Seconds()
	if seconds <= 0 {
		seconds = 1
	}

	stats := make([]NetworkStat, 0, len(after))
	for name, current := range after {
		previous := before[name]
		if current.RxBytes >= previous.RxBytes {
			current.RxBytesRate = uint64(float64(current.RxBytes-previous.RxBytes) / seconds)
		}
		if current.TxBytes >= previous.TxBytes {
			current.TxBytesRate = uint64(float64(current.TxBytes-previous.TxBytes) / seconds)
		}
		stats = append(stats, current)
	}
	return stats
}

func unescapeMountField(value string) string {
	replacer := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	return replacer.Replace(value)
}

func roundPct(value float64) float64 {
	return math.Round(value*100) / 100
}

func procPath(name string) string {
	return defaultProcRoot + "/" + strings.TrimPrefix(name, "/")
}
