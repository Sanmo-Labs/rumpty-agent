package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/Sanmo-Labs/rumpty-agent/internal/metrics"
	"github.com/Sanmo-Labs/rumpty-agent/internal/transport"
)

const (
	DefaultHostCID        = 2
	DefaultPort           = 5000
	DefaultSampleWindow   = time.Second
	DefaultSampleInterval = 15 * time.Second
	DefaultFlushInterval  = 30 * time.Second
	DefaultMaxBatchSize   = 8
	MaxSampleWindow       = 10 * time.Second
	MaxSampleInterval     = 5 * time.Minute
	MaxFlushInterval      = 5 * time.Minute
	DefaultShutdownFlush  = 5 * time.Second
)

type DaemonConfig struct {
	AgentVersion   string
	AgentCommit    string
	HostCID        uint32
	Port           uint32
	Root           string
	SampleWindow   time.Duration
	SampleInterval time.Duration
	FlushInterval  time.Duration
	ConnectTimeout time.Duration
	WriteTimeout   time.Duration
	ShutdownFlush  time.Duration
	MaxBatchSize   int
	Dial           func(context.Context, transport.VSOCKConfig) (transport.WriteConn, error)
	Logger         *log.Logger
}

type MetricsBatch struct {
	SchemaVersion string             `json:"schema_version"`
	AgentVersion  string             `json:"agent_version"`
	AgentCommit   string             `json:"agent_commit,omitempty"`
	SentAt        time.Time          `json:"sent_at"`
	Samples       []metrics.Snapshot `json:"samples"`
}

func RunDaemon(ctx context.Context, cfg DaemonConfig) error {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return err
	}

	cfg.Logger.Printf("rumpty-agent daemon starting host_cid=%d port=%d sample_interval=%s flush_interval=%s", cfg.HostCID, cfg.Port, cfg.SampleInterval, cfg.FlushInterval)

	var batch []metrics.Snapshot
	var nextFlush = time.Now().Add(cfg.FlushInterval)
	var failures int

	for {
		select {
		case <-ctx.Done():
			if len(batch) > 0 {
				flushCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownFlush)
				if err := pushBatch(flushCtx, cfg, batch); err != nil {
					cfg.Logger.Printf("final metrics push failed: %v", err)
				}
				cancel()
			}
			return nil
		default:
		}

		snapshot, err := metrics.Collect(ctx, metrics.Options{
			Root:         cfg.Root,
			SampleWindow: cfg.SampleWindow,
		})
		if err != nil {
			cfg.Logger.Printf("collect metrics failed: %v", err)
		} else {
			var dropped bool
			batch, dropped = appendBounded(batch, snapshot, cfg.MaxBatchSize)
			if dropped {
				cfg.Logger.Printf("metrics batch full; dropped oldest sample after previous push failures")
			}
		}

		shouldFlush := len(batch) >= cfg.MaxBatchSize || (len(batch) > 0 && !time.Now().Before(nextFlush))
		if shouldFlush {
			if err := pushBatch(ctx, cfg, batch); err != nil {
				failures++
				cfg.Logger.Printf("metrics push failed: %v", err)
				sleep := backoff(failures)
				if err := sleepContext(ctx, sleep); err != nil {
					return nil
				}
			} else {
				failures = 0
				batch = batch[:0]
				nextFlush = time.Now().Add(cfg.FlushInterval)
			}
		}

		if err := sleepContext(ctx, cfg.SampleInterval); err != nil {
			return nil
		}
	}
}

func pushBatch(ctx context.Context, cfg DaemonConfig, samples []metrics.Snapshot) error {
	conn, err := cfg.Dial(ctx, transport.VSOCKConfig{
		CID:     cfg.HostCID,
		Port:    cfg.Port,
		Timeout: cfg.ConnectTimeout,
	})
	if err != nil {
		return fmt.Errorf("dial vsock: %w", err)
	}
	defer conn.Close()

	if cfg.WriteTimeout > 0 {
		_ = conn.SetWriteDeadline(time.Now().Add(cfg.WriteTimeout))
	}

	batch := MetricsBatch{
		SchemaVersion: "rumpty.agent.metrics.batch.v1",
		AgentVersion:  cfg.AgentVersion,
		AgentCommit:   cfg.AgentCommit,
		SentAt:        time.Now().UTC(),
		Samples:       samples,
	}

	enc := json.NewEncoder(conn)
	if err := enc.Encode(batch); err != nil {
		return fmt.Errorf("write metrics batch: %w", err)
	}
	return nil
}

func (cfg DaemonConfig) withDefaults() DaemonConfig {
	if cfg.HostCID == 0 {
		cfg.HostCID = DefaultHostCID
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.Root == "" {
		cfg.Root = "/"
	}
	if cfg.SampleWindow == 0 {
		cfg.SampleWindow = DefaultSampleWindow
	}
	if cfg.SampleInterval == 0 {
		cfg.SampleInterval = DefaultSampleInterval
	}
	if cfg.FlushInterval == 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 5 * time.Second
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	if cfg.ShutdownFlush == 0 {
		cfg.ShutdownFlush = DefaultShutdownFlush
	}
	if cfg.MaxBatchSize == 0 {
		cfg.MaxBatchSize = DefaultMaxBatchSize
	}
	if cfg.Dial == nil {
		cfg.Dial = transport.DialVSOCK
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(io.Discard, "", 0)
	}
	return cfg
}

func (cfg DaemonConfig) validate() error {
	if cfg.SampleWindow < 100*time.Millisecond {
		return fmt.Errorf("sample-window must be at least 100ms")
	}
	if cfg.SampleWindow > MaxSampleWindow {
		return fmt.Errorf("sample-window must not exceed %s", MaxSampleWindow)
	}
	if cfg.SampleInterval < cfg.SampleWindow {
		return fmt.Errorf("sample-interval must be greater than or equal to sample-window")
	}
	if cfg.SampleInterval > MaxSampleInterval {
		return fmt.Errorf("sample-interval must not exceed %s", MaxSampleInterval)
	}
	if cfg.FlushInterval < cfg.SampleInterval {
		return fmt.Errorf("flush-interval must be greater than or equal to sample-interval")
	}
	if cfg.FlushInterval > MaxFlushInterval {
		return fmt.Errorf("flush-interval must not exceed %s", MaxFlushInterval)
	}
	if cfg.MaxBatchSize < 1 {
		return fmt.Errorf("max-batch-size must be greater than zero")
	}
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func appendBounded(batch []metrics.Snapshot, snapshot metrics.Snapshot, limit int) ([]metrics.Snapshot, bool) {
	if limit < 1 {
		limit = 1
	}
	if len(batch) < limit {
		return append(batch, snapshot), false
	}

	copy(batch, batch[1:])
	batch[len(batch)-1] = snapshot
	return batch, true
}

func backoff(failures int) time.Duration {
	if failures < 1 {
		failures = 1
	}
	shift := min(failures-1, 5)
	seconds := 1 << shift
	if seconds > 30 {
		seconds = 30
	}
	return time.Duration(seconds) * time.Second
}
