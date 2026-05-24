package metrics

import "time"

type Snapshot struct {
	SchemaVersion string           `json:"schema_version"`
	CollectedAt   time.Time        `json:"collected_at"`
	Host          HostInfo         `json:"host"`
	CPU           CPUStats         `json:"cpu"`
	Memory        MemoryStats      `json:"memory"`
	Filesystems   []FilesystemStat `json:"filesystems"`
	Network       []NetworkStat    `json:"network"`
}

type HostInfo struct {
	Hostname string  `json:"hostname"`
	BootID   string  `json:"boot_id,omitempty"`
	Uptime   float64 `json:"uptime_seconds,omitempty"`
}

type CPUStats struct {
	OnlineCores int     `json:"online_cores"`
	UsagePct    float64 `json:"usage_pct"`
}

type MemoryStats struct {
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPct        float64 `json:"used_pct"`
}

type FilesystemStat struct {
	Mountpoint     string  `json:"mountpoint"`
	FilesystemType string  `json:"filesystem_type"`
	Device         string  `json:"device"`
	TotalBytes     uint64  `json:"total_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	UsedPct        float64 `json:"used_pct"`
}

type NetworkStat struct {
	Interface   string `json:"interface"`
	RxBytes     uint64 `json:"rx_bytes"`
	TxBytes     uint64 `json:"tx_bytes"`
	RxBytesRate uint64 `json:"rx_bytes_per_second"`
	TxBytesRate uint64 `json:"tx_bytes_per_second"`
}
