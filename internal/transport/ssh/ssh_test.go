package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/darnodo/NetMapper/internal/secret"
	"github.com/darnodo/NetMapper/internal/transport"
)

// server is a minimal device: it echoes input and answers a few commands after a "sw1>" prompt.
func server(t *testing.T) int {
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := gossh.NewSignerFromKey(key)
	cfg := &gossh.ServerConfig{PasswordCallback: func(c gossh.ConnMetadata, pw []byte) (*gossh.Permissions, error) {
		if c.User() == "nm" && string(pw) == "good" {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := gossh.NewServerConn(c, cfg)
				if err != nil {
					return
				}
				go gossh.DiscardRequests(reqs)
				for nc := range chans {
					ch, reqs, _ := nc.Accept()
					go func() {
						for r := range reqs {
							r.Reply(r.Type == "pty-req" || r.Type == "shell", nil)
						}
					}()
					go shell(ch)
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port
}

func shell(ch gossh.Channel) {
	defer ch.Close()
	fmt.Fprint(ch, "sw1>")
	var line []byte
	b := make([]byte, 1)
	for {
		if _, err := ch.Read(b); err != nil {
			return
		}
		if b[0] != '\n' && b[0] != '\r' {
			ch.Write(b)
			line = append(line, b[0])
			continue
		}
		switch cmd := strings.TrimSpace(string(line)); cmd {
		case "show version":
			fmt.Fprint(ch, "\nTest OS 1.0\nsw1>")
		case "show trickle":
			fmt.Fprint(ch, "\n")
			for i := range 8 {
				fmt.Fprintf(ch, "line %d\n", i)
				time.Sleep(50 * time.Millisecond)
			}
			fmt.Fprint(ch, "sw1>")
		case "show stall":
			fmt.Fprint(ch, "\npartial\n")
			select {} // never finishes
		default:
			fmt.Fprint(ch, "\nsw1>")
		}
		line = line[:0]
	}
}

func open(t *testing.T, port int, password string) (transport.Session, error) {
	t.Setenv("NM_TEST_SSH", password)
	sec, _ := secret.NewResolver().Resolve(context.Background(), "env:NM_TEST_SSH")
	tr := &Transport{Port: port, SocketTimeout: 2 * time.Second}
	return tr.Open(context.Background(), transport.Target{Addr: netip.MustParseAddr("127.0.0.1")},
		transport.Credential{Set: "a", Kind: "ssh", Username: "nm", Secret: sec})
}

func TestOpen(t *testing.T) {
	port := server(t)
	_, err := open(t, port, "bad")
	var ae *transport.AuthError
	if !errors.As(err, &ae) || len(ae.Evidence) == 0 {
		t.Errorf("wrong password: %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	closed := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	if _, err := open(t, closed, "good"); !errors.Is(err, transport.ErrSilent) {
		t.Errorf("closed port: %v", err)
	}
}

func TestRun(t *testing.T) {
	port := server(t)
	run := func(cmd string, idle, deadline time.Duration) (string, error) {
		s, err := open(t, port, "good")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		out, err := s.Run(context.Background(), transport.Step{Command: cmd, IdleTimeout: idle, Deadline: deadline})
		return string(out.Bytes), err
	}
	if out, err := run("show version", time.Second, 5*time.Second); err != nil || !strings.Contains(out, "Test OS 1.0") {
		t.Errorf("show version: %q %v", out, err)
	}
	// Output that keeps arriving never trips the idle timeout, however long it takes overall.
	if out, err := run("show trickle", 200*time.Millisecond, 5*time.Second); err != nil || !strings.Contains(out, "line 7") {
		t.Errorf("trickle: %q %v", out, err)
	}
	start := time.Now()
	if _, err := run("show stall", 200*time.Millisecond, 5*time.Second); !errors.Is(err, transport.ErrIdleTimeout) || time.Since(start) > 3*time.Second {
		t.Errorf("stall: %v after %s", err, time.Since(start))
	}
	if _, err := run("show trickle", 5*time.Second, 150*time.Millisecond); !errors.Is(err, transport.ErrDeadline) {
		t.Errorf("deadline: %v", err)
	}
}
