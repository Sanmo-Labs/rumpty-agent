//go:build linux

package transport

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

func dialVSOCK(ctx context.Context, cid, port uint32) (WriteConn, error) {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return nil, fmt.Errorf("vsock socket: %w", err)
	}
	if err := unix.Connect(fd, &unix.SockaddrVM{CID: cid, Port: port}); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("vsock connect cid=%d port=%d: %w", cid, port, err)
	}
	f := os.NewFile(uintptr(fd), fmt.Sprintf("vsock-cid%d-port%d", cid, port))
	return &rawConn{f: f}, nil
}

func listenVSOCK(ctx context.Context, cfg ListenVSOCKConfig, handler ConnHandler) error {
	fd, err := unix.Socket(unix.AF_VSOCK, unix.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("vsock socket: %w", err)
	}

	if err := unix.Bind(fd, &unix.SockaddrVM{CID: unix.VMADDR_CID_ANY, Port: cfg.Port}); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock bind port %d: %w", cfg.Port, err)
	}
	if err := unix.Listen(fd, 8); err != nil {
		_ = unix.Close(fd)
		return fmt.Errorf("vsock listen: %w", err)
	}

	go func() {
		<-ctx.Done()
		_ = unix.Close(fd)
	}()

	for {
		connFD, _, err := unix.Accept(fd)
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				return fmt.Errorf("vsock accept: %w", err)
			}
		}
		// net.FileConn calls getsockname which fails for AF_VSOCK — wrap directly.
		conn := &rawConn{f: os.NewFile(uintptr(connFD), "vsock-conn")}
		go handler(conn)
	}
}

// rawConn wraps an os.File as a net.Conn without any socket-type introspection.
type rawConn struct {
	f *os.File
}

func (c *rawConn) Read(b []byte) (int, error)         { return c.f.Read(b) }
func (c *rawConn) Write(b []byte) (int, error)        { return c.f.Write(b) }
func (c *rawConn) Close() error                       { return c.f.Close() }
func (c *rawConn) LocalAddr() net.Addr                { return vsockAddr{} }
func (c *rawConn) RemoteAddr() net.Addr               { return vsockAddr{} }
func (c *rawConn) SetDeadline(t time.Time) error      { return c.f.SetDeadline(t) }
func (c *rawConn) SetReadDeadline(t time.Time) error  { return c.f.SetReadDeadline(t) }
func (c *rawConn) SetWriteDeadline(t time.Time) error { return c.f.SetWriteDeadline(t) }

type vsockAddr struct{}

func (vsockAddr) Network() string { return "vsock" }
func (vsockAddr) String() string  { return "vsock" }
