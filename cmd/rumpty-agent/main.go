package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Sanmo-Labs/rumpty-agent/internal/agent"
	"github.com/Sanmo-Labs/rumpty-agent/internal/metrics"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "rumpty-agent: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		printUsage(stderr)
		return errors.New("missing command")
	}

	switch args[0] {
	case "daemon":
		return runDaemon(ctx, args[1:], stderr)
	case "metrics":
		return runMetrics(ctx, args[1:], stdout)
	case "version":
		fmt.Fprintf(stdout, "rumpty-agent %s commit=%s built=%s\n", version, commit, date)
		return nil
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stderr)
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runDaemon(ctx context.Context, args []string, stderr io.Writer) error {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var hostCID uint
	var port uint
	var root string
	var sampleWindow time.Duration
	var sampleInterval time.Duration
	var flushInterval time.Duration
	var connectTimeout time.Duration
	var writeTimeout time.Duration
	var shutdownFlush time.Duration
	var maxBatchSize int

	fs.UintVar(&hostCID, "vsock-cid", agent.DefaultHostCID, "host VSOCK CID")
	fs.UintVar(&port, "vsock-port", agent.DefaultPort, "collector VSOCK port")
	fs.StringVar(&root, "root", "/", "root filesystem path to inspect")
	fs.DurationVar(&sampleWindow, "sample-window", agent.DefaultSampleWindow, "network and CPU sample window")
	fs.DurationVar(&sampleInterval, "sample-interval", agent.DefaultSampleInterval, "metrics collection interval")
	fs.DurationVar(&flushInterval, "flush-interval", agent.DefaultFlushInterval, "metrics push interval")
	fs.DurationVar(&connectTimeout, "connect-timeout", 5*time.Second, "VSOCK connect timeout")
	fs.DurationVar(&writeTimeout, "write-timeout", 5*time.Second, "VSOCK write timeout")
	fs.DurationVar(&shutdownFlush, "shutdown-flush-timeout", agent.DefaultShutdownFlush, "maximum time to spend on final metrics push during shutdown")
	fs.IntVar(&maxBatchSize, "max-batch-size", agent.DefaultMaxBatchSize, "maximum samples per pushed batch")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if hostCID > uint(^uint32(0)) {
		return errors.New("vsock-cid is too large")
	}
	if port > uint(^uint32(0)) {
		return errors.New("vsock-port is too large")
	}

	logger := log.New(stderr, "rumpty-agent: ", log.LstdFlags|log.LUTC)
	return agent.RunDaemon(ctx, agent.DaemonConfig{
		AgentVersion:   version,
		AgentCommit:    commit,
		HostCID:        uint32(hostCID),
		Port:           uint32(port),
		Root:           root,
		SampleWindow:   sampleWindow,
		SampleInterval: sampleInterval,
		FlushInterval:  flushInterval,
		ConnectTimeout: connectTimeout,
		WriteTimeout:   writeTimeout,
		ShutdownFlush:  shutdownFlush,
		MaxBatchSize:   maxBatchSize,
		Logger:         logger,
	})
}

func runMetrics(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New("metrics requires a subcommand: once")
	}

	switch args[0] {
	case "once":
		fs := flag.NewFlagSet("metrics once", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var pretty bool
		var sampleWindow time.Duration
		var root string
		fs.BoolVar(&pretty, "pretty", false, "pretty-print JSON")
		fs.DurationVar(&sampleWindow, "sample-window", time.Second, "network and CPU sample window")
		fs.StringVar(&root, "root", "/", "root filesystem path to inspect")

		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if sampleWindow < 100*time.Millisecond {
			return errors.New("sample-window must be at least 100ms")
		}
		if sampleWindow > agent.MaxSampleWindow {
			return fmt.Errorf("sample-window must not exceed %s", agent.MaxSampleWindow)
		}

		snapshot, err := metrics.Collect(ctx, metrics.Options{
			Root:         root,
			SampleWindow: sampleWindow,
		})
		if err != nil {
			return err
		}

		enc := json.NewEncoder(stdout)
		if pretty {
			enc.SetIndent("", "  ")
		}
		return enc.Encode(snapshot)
	default:
		return fmt.Errorf("unknown metrics subcommand %q", args[0])
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, strings.TrimSpace(`
Rumpty guest agent.

Usage:
  rumpty-agent daemon [--vsock-cid 2] [--vsock-port 5000]
  rumpty-agent metrics once [--pretty] [--sample-window 1s]
  rumpty-agent version

The agent only reports local guest telemetry. It does not read user files,
environment variables, shell history, SSH keys, or application secrets.
`))
}
