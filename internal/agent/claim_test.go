package agent

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

type panicClaimConn struct {
	writes bytes.Buffer
	closed bool
}

func (c *panicClaimConn) Read([]byte) (int, error) {
	panic("boom")
}

func (c *panicClaimConn) Write(p []byte) (int, error) {
	return c.writes.Write(p)
}

func (c *panicClaimConn) Close() error {
	c.closed = true
	return nil
}

func TestHandleClaimConnRecoversPanic(t *testing.T) {
	conn := &panicClaimConn{}
	var logs bytes.Buffer

	HandleClaimConn(ClaimServerConfig{
		Logger: log.New(&logs, "", 0),
	}, conn)

	if !conn.closed {
		t.Fatal("connection was not closed")
	}
	if got := conn.writes.String(); !strings.Contains(got, "err: panic while handling claim: boom") || !strings.Contains(got, "goroutine") {
		t.Fatalf("response = %q, want panic error", got)
	}
	if got := logs.String(); !strings.Contains(got, "panic while handling claim payload: boom") {
		t.Fatalf("logs = %q, want panic log", got)
	}
}
