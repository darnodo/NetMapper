// Package ssh is the SSH transport, driven by scrapligo. It sends only Step.Command, never enters
// a configuration mode, and dials only the address it is given (from perimeter.Resolve).
package ssh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/scrapli/scrapligo/driver/generic"
	"github.com/scrapli/scrapligo/driver/opoptions"
	"github.com/scrapli/scrapligo/driver/options"
	"github.com/scrapli/scrapligo/platform"
	"github.com/scrapli/scrapligo/response"
	"github.com/scrapli/scrapligo/util"

	"github.com/darnodo/NetMapper/internal/transport"
)

// Transport opens sessions with the scrapligo platform definition of the target's pack, or with
// the generic driver while the platform is still unknown (fingerprinting).
type Transport struct {
	// Platforms maps a pack name to its scrapli.yaml. Keys are pack names from PackRegistry.
	Platforms     map[string][]byte
	Port          int
	SocketTimeout time.Duration
}

type driver interface {
	SendCommand(command string, opts ...util.Option) (*response.Response, error)
	Close() error
}

func (t *Transport) Open(ctx context.Context, target transport.Target, cred transport.Credential) (transport.Session, error) {
	act := make(activity, 1)
	opts := []util.Option{
		options.WithTransportType("standard"),
		options.WithPort(t.Port),
		options.WithTimeoutSocket(t.SocketTimeout),
		options.WithAuthUsername(cred.Username),
		options.WithAuthPassword(cred.Secret.Value()),
		// ponytail: host keys are not pinned; a discovery tool meets hosts for the first time.
		// Add a known_hosts store per snapshot if key continuity must be audited.
		options.WithAuthNoStrictKey(),
		options.WithTermWidth(32767),
		options.WithChannelLog(act),
	}
	host := target.Addr.String()
	var d driver
	var open func() error
	if def := t.Platforms[target.Platform]; def != nil {
		p, err := platform.NewPlatform(def, host, opts...)
		if err != nil {
			return nil, err
		}
		nd, err := p.GetNetworkDriver()
		if err != nil {
			return nil, err
		}
		d, open = nd, nd.Open
	} else {
		gd, err := generic.NewDriver(host, opts...)
		if err != nil {
			return nil, err
		}
		// The generic driver sends the first command as soon as the channel is up. A CLI that is
		// still starting echoes it, drops it and prints its prompt, and the echo comes back as the
		// whole answer (issue #15). Wait for the first prompt; nothing is sent to the device.
		d, open = gd, func() error {
			if err := gd.Open(); err != nil {
				return err
			}
			pctx, cancel := context.WithTimeout(ctx, t.SocketTimeout)
			defer cancel()
			if _, err := gd.Channel.ReadUntilPrompt(pctx); err != nil {
				gd.Close()
				return fmt.Errorf("%w: no prompt after login", transport.ErrIdleTimeout)
			}
			return nil
		}
	}
	opened := make(chan error, 1)
	go func() { opened <- open() }()
	select {
	case err := <-opened:
		if err != nil {
			return nil, classifyOpen(err)
		}
	case <-ctx.Done():
		go func() {
			if <-opened == nil {
				d.Close()
			}
		}()
		return nil, ctx.Err()
	}
	return &session{d: d, act: act}, nil
}

func classifyOpen(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "unable to authenticate"), errors.Is(err, util.ErrAuthError):
		// The server's rejection is the evidence that it answered (FR-022).
		return &transport.AuthError{Evidence: []byte(msg)}
	case transport.NoAnswer(err), errors.Is(err, util.ErrTimeoutError):
		return transport.ErrSilent
	}
	return err
}

// activity signals every chunk the channel reads, which drives the idle timer.
type activity chan struct{}

func (a activity) Write(b []byte) (int, error) {
	select {
	case a <- struct{}{}:
	default:
	}
	return len(b), nil
}

type session struct {
	d   driver
	act activity
}

// Run sends one command. scrapligo's operation timeout is a total, so it carries the step
// deadline; the idle timeout is a timer reset by every chunk read, which closes the session when
// the device stops sending. There is no output size cap (research R12).
func (s *session) Run(ctx context.Context, step transport.Step) (transport.RawOutput, error) {
	select {
	case <-s.act:
	default:
	}
	idle := step.IdleTimeout
	if idle <= 0 {
		idle = 24 * time.Hour
	}
	var idled atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTimer(idle)
		defer t.Stop()
		for {
			select {
			case <-s.act:
				t.Reset(idle)
			case <-t.C:
				idled.Store(true)
				s.d.Close()
				return
			case <-ctx.Done():
				s.d.Close()
				return
			case <-done:
				return
			}
		}
	}()
	var opts []util.Option
	if step.Deadline > 0 {
		opts = append(opts, opoptions.WithTimeoutOps(step.Deadline))
	}
	resp, err := s.d.SendCommand(step.Command, opts...)
	switch {
	case idled.Load():
		return transport.RawOutput{}, transport.ErrIdleTimeout
	case ctx.Err() != nil:
		return transport.RawOutput{}, ctx.Err()
	case errors.Is(err, util.ErrTimeoutError):
		return transport.RawOutput{}, transport.ErrDeadline
	case err != nil:
		return transport.RawOutput{}, err
	}
	// ponytail: the whole output of one step is held in memory once, then hashed, uploaded and
	// parsed from the same buffer (research R12). Upgrade path if a table proves too large: read
	// at the scrapligo channel level, stream to the object store while hashing, parse a re-read.
	return transport.RawOutput{Bytes: dropEcho(resp.RawResult, step.Command)}, nil
}

// dropEcho removes the first line of out when it is the command's own echo. On EOS, the first
// command of a session sometimes comes back preceded by a late prompt and its echo
// ("sw2#show aaa methods all | no-more"), or by the tail of the echo ("aa methods all | no-more"):
// the prompt the platform driver read on open was not the last one the device printed. Issue #15
// fixed the same race for the generic driver only. Seen on cEOS 4.36 on test/lab (feature 007,
// research R13). The rule is narrow so real output is never cut: the line must end with the whole
// command, or be a suffix of it at least 8 characters long.
func dropEcho(out []byte, command string) []byte {
	line, rest, found := bytes.Cut(out, []byte("\n"))
	first := strings.TrimSpace(string(line))
	if first == "" || command == "" {
		return out
	}
	if strings.HasSuffix(first, command) || (len(first) >= 8 && strings.HasSuffix(command, first)) {
		if !found {
			return []byte{}
		}
		return rest
	}
	return out
}

func (s *session) Close() error { return s.d.Close() }
