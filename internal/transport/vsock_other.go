//go:build !linux

package transport

import (
	"context"
	"errors"
)

func dialVSOCK(_ context.Context, _, _ uint32) (WriteConn, error) {
	return nil, errors.New("vsock is only supported on Linux")
}

func listenVSOCK(_ context.Context, _ ListenVSOCKConfig, _ ConnHandler) error {
	return errors.New("vsock is only supported on Linux")
}
