package verifier

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Runner runs verifier/runner.mjs under node, one process per compile, with
// a timeout and at most a fixed number of compiles at a time.
type Runner struct {
	Node      string        // NODE_BIN
	Script    string        // RUNNER_PATH
	Timeout   time.Duration // COMPILE_TIMEOUT_SECONDS
	MaxOutput int           // bytes of compiler output accepted

	slots chan struct{}
}

// NewRunner returns a runner allowing max compiles at once.
func NewRunner(node, script string, timeout time.Duration, max int) *Runner {
	if max < 1 {
		max = 1
	}
	return &Runner{Node: node, Script: script, Timeout: timeout, MaxOutput: 256 << 20, slots: make(chan struct{}, max)}
}

// ErrTimeout is returned when a compile ran past the timeout.
var ErrTimeout = errors.New("compile timed out")

// Compile feeds input (standard JSON) to the compiler at soljson and returns
// its output JSON. It waits for a free slot first; ctx ending while waiting
// or compiling stops it (the node process is killed).
func (r *Runner) Compile(ctx context.Context, soljson string, input []byte) ([]byte, error) {
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.slots }()

	cctx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, r.Node, r.Script, soljson)
	cmd.Stdin = bytes.NewReader(input)
	stdout := &capped{max: r.MaxOutput}
	stderr := &capped{max: 64 << 10, keepTail: true}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Only PATH: the compiler needs nothing else, and nothing from the
	// service's environment should reach a process that runs downloaded code.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	cmd.WaitDelay = 5 * time.Second

	err := cmd.Run()
	switch {
	case cctx.Err() == context.DeadlineExceeded && ctx.Err() == nil:
		return nil, fmt.Errorf("%w after %s", ErrTimeout, r.Timeout)
	case ctx.Err() != nil:
		return nil, ctx.Err()
	case stdout.over:
		return nil, fmt.Errorf("compiler output larger than %d bytes", r.MaxOutput)
	case err != nil:
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("compiler failed: %s", msg)
	}
	return stdout.buf.Bytes(), nil
}

// capped is an io.Writer that keeps at most max bytes: the head (and notes
// that it overflowed), or with keepTail the most recent bytes.
type capped struct {
	buf      bytes.Buffer
	max      int
	over     bool
	keepTail bool
}

func (c *capped) Write(p []byte) (int, error) {
	n := len(p)
	if c.keepTail {
		c.buf.Write(p)
		if c.buf.Len() > c.max {
			tail := append([]byte(nil), c.buf.Bytes()[c.buf.Len()-c.max:]...)
			c.buf.Reset()
			c.buf.Write(tail)
		}
		return n, nil
	}
	if room := c.max - c.buf.Len(); len(p) > room {
		c.over = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
		return n, nil
	}
	c.buf.Write(p)
	return n, nil
}

func (c *capped) String() string { return c.buf.String() }
