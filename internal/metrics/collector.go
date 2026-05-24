package metrics

import (
	"context"
	"time"
)

type Options struct {
	Root         string
	SampleWindow time.Duration
}

func Collect(ctx context.Context, opts Options) (Snapshot, error) {
	if opts.Root == "" {
		opts.Root = "/"
	}
	if opts.SampleWindow == 0 {
		opts.SampleWindow = time.Second
	}

	return collectLinux(ctx, opts)
}
