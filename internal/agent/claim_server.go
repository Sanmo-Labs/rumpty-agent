package agent

import (
	"context"
	"log"

	"github.com/Sanmo-Labs/rumpty-agent/internal/transport"
)

// RunClaimServer starts the inbound claim listener until ctx is cancelled.
func RunClaimServer(ctx context.Context, cfg ClaimServerConfig) error {
	port := cfg.Port
	if port == 0 {
		port = DefaultClaimPort
	}
	logger := cfg.Logger
	if logger == nil {
		logger = log.Default()
	}

	logger.Printf("claim server listening on VSOCK port %d", port)

	return transport.ListenVSOCK(ctx, transport.ListenVSOCKConfig{
		Port: port,
	}, func(conn interface {
		Read([]byte) (int, error)
		Write([]byte) (int, error)
		Close() error
	}) {
		HandleClaimConn(cfg, conn)
	})
}
