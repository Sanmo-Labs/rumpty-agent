//go:build linux

package transport

import (
	"context"

	"github.com/mdlayher/socket"
	"golang.org/x/sys/unix"
)

type addrlessConn struct {
	*socket.Conn
}

func dialVSOCK(ctx context.Context, cid, port uint32) (WriteConn, error) {
	conn, err := socket.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0, "vsock", nil)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Connect(ctx, &unix.SockaddrVM{CID: cid, Port: port}); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return addrlessConn{Conn: conn}, nil
}
