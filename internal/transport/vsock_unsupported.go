//go:build !linux

package transport

import (
	"context"
	"errors"
)

func dialVSOCK(_ context.Context, _, _ uint32) (WriteConn, error) {
	return nil, errors.New("vsock transport is only supported on Linux")
}
