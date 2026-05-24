package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/Sanmo-Labs/rumpty-agent/internal/metrics"
	"github.com/Sanmo-Labs/rumpty-agent/internal/transport"
)

func TestDaemonConfigValidate(t *testing.T) {
	cfg := DaemonConfig{
		SampleWindow:   time.Second,
		SampleInterval: 500 * time.Millisecond,
		FlushInterval:  time.Second,
		MaxBatchSize:   1,
	}

	if err := cfg.validate(); err == nil {
		t.Fatal("validate() error = nil, want error")
	}
}

func TestPushBatchWritesMetricsEnvelope(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()

	var received bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := received.ReadFrom(server)
		done <- err
	}()

	cfg := DaemonConfig{
		AgentVersion: "test",
		AgentCommit:  "abc123",
		WriteTimeout: time.Second,
		Dial: func(context.Context, transport.VSOCKConfig) (transport.WriteConn, error) {
			return client, nil
		},
	}.withDefaults()

	err := pushBatch(context.Background(), cfg, []metrics.Snapshot{
		{SchemaVersion: "rumpty.agent.metrics.v1", CollectedAt: time.Unix(1, 0).UTC()},
	})
	if err != nil {
		t.Fatalf("pushBatch() error = %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("server read error = %v", err)
	}

	var batch MetricsBatch
	if err := json.Unmarshal(received.Bytes(), &batch); err != nil {
		t.Fatalf("unmarshal batch: %v", err)
	}
	if batch.SchemaVersion != "rumpty.agent.metrics.batch.v1" {
		t.Fatalf("SchemaVersion = %q", batch.SchemaVersion)
	}
	if batch.AgentVersion != "test" {
		t.Fatalf("AgentVersion = %q", batch.AgentVersion)
	}
	if len(batch.Samples) != 1 {
		t.Fatalf("len(Samples) = %d, want 1", len(batch.Samples))
	}
}

func TestAppendBoundedDropsOldest(t *testing.T) {
	first := metrics.Snapshot{SchemaVersion: "first"}
	second := metrics.Snapshot{SchemaVersion: "second"}
	third := metrics.Snapshot{SchemaVersion: "third"}

	batch, dropped := appendBounded([]metrics.Snapshot{first, second}, third, 2)
	if !dropped {
		t.Fatal("dropped = false, want true")
	}
	if len(batch) != 2 {
		t.Fatalf("len(batch) = %d, want 2", len(batch))
	}
	if batch[0].SchemaVersion != "second" || batch[1].SchemaVersion != "third" {
		t.Fatalf("batch = %#v, want second/third", batch)
	}
}
