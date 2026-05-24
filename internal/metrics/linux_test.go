package metrics

import (
	"testing"
	"time"
)

func TestCPUUsagePct(t *testing.T) {
	before := cpuSample{idle: 100, total: 200}
	after := cpuSample{idle: 150, total: 400}

	got := cpuUsagePct(before, after)
	if got != 75 {
		t.Fatalf("cpuUsagePct() = %v, want 75", got)
	}
}

func TestNetworkRates(t *testing.T) {
	before := netSample{
		"eth0": {Interface: "eth0", RxBytes: 100, TxBytes: 200},
	}
	after := netSample{
		"eth0": {Interface: "eth0", RxBytes: 300, TxBytes: 260},
	}

	got := networkRates(before, after, 2*time.Second)
	if len(got) != 1 {
		t.Fatalf("len(networkRates()) = %d, want 1", len(got))
	}
	if got[0].RxBytesRate != 100 {
		t.Fatalf("RxBytesRate = %d, want 100", got[0].RxBytesRate)
	}
	if got[0].TxBytesRate != 30 {
		t.Fatalf("TxBytesRate = %d, want 30", got[0].TxBytesRate)
	}
}

func TestUnescapeMountField(t *testing.T) {
	got := unescapeMountField(`/mnt/rumpty\040data`)
	if got != "/mnt/rumpty data" {
		t.Fatalf("unescapeMountField() = %q, want %q", got, "/mnt/rumpty data")
	}
}

func TestMountUnderRoot(t *testing.T) {
	tests := []struct {
		name       string
		mountpoint string
		root       string
		want       bool
	}{
		{name: "root includes all", mountpoint: "/mnt/data", root: "/", want: true},
		{name: "exact", mountpoint: "/mnt", root: "/mnt", want: true},
		{name: "child", mountpoint: "/mnt/data", root: "/mnt", want: true},
		{name: "prefix sibling", mountpoint: "/mntain", root: "/mnt", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mountUnderRoot(tt.mountpoint, tt.root)
			if got != tt.want {
				t.Fatalf("mountUnderRoot(%q, %q) = %v, want %v", tt.mountpoint, tt.root, got, tt.want)
			}
		})
	}
}
