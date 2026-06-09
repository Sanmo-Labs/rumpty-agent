package transport

import (
	"context"
	"io"
	"time"
)

// WriteConn is a write connection to the host collector.
type WriteConn interface {
	io.WriteCloser
	SetWriteDeadline(time.Time) error
}

// VSOCKConfig holds dial parameters.
type VSOCKConfig struct {
	CID     uint32
	Port    uint32
	Timeout time.Duration
}

// ConnHandler handles an accepted connection.
type ConnHandler func(conn interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	Close() error
})

// ListenVSOCKConfig holds listener configuration.
type ListenVSOCKConfig struct {
	Port    uint32
	Timeout time.Duration
}

// DialVSOCK connects to the host VSOCK collector.
func DialVSOCK(ctx context.Context, cfg VSOCKConfig) (WriteConn, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	dialCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	return dialVSOCK(dialCtx, cfg.CID, cfg.Port)
}

// ListenVSOCK starts a VSOCK listener on cfg.Port.
func ListenVSOCK(ctx context.Context, cfg ListenVSOCKConfig, handler ConnHandler) error {
	return listenVSOCK(ctx, cfg, handler)
}
