package transport

import (
	"context"
	"io"
	"time"
)

type WriteConn interface {
	io.WriteCloser
	SetWriteDeadline(time.Time) error
}

type VSOCKConfig struct {
	CID     uint32
	Port    uint32
	Timeout time.Duration
}

func DialVSOCK(ctx context.Context, cfg VSOCKConfig) (WriteConn, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	return dialVSOCK(ctx, cfg.CID, cfg.Port)
}
