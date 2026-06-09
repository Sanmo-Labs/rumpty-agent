//go:build linux

package transport

import (
	"context"
	"fmt"
	"net"
	"os"

	"github.com/mdlayher/socket"
	"golang.org/x/sys/unix"
)

func dialVSOCK(ctx context.Context, cid, port uint32) (WriteConn, error) {
	conn, err := socket.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0, "vsock", nil)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if _, err := conn.Connect(ctx, &unix.SockaddrVM{CID: cid, Port: port}); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", cid, port, err)
	}
	return conn, nil
}

func listenVSOCK(ctx context.Context, cfg ListenVSOCKConfig, handler ConnHandler) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}

	addr := &unix.SockaddrVM{
		CID:  unix.VMADDR_CID_ANY,
		Port: cfg.Port,
	}
	if err := unix.Bind(fd, addr); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock bind port %d: %w", cfg.Port, err)
	}
	if err := unix.Listen(fd, 8); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock listen: %w", err)
	}

	file := os.NewFile(uintptr(fd), "vsock")
	ln, err := net.FileListener(file)
	_ = file.Close()
	if err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("wrap vsock listener: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("vsock accept: %w", err)
			}
		}
		go handler(conn)
	}
}
