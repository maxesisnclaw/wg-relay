package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

type Relay struct {
	mu      sync.Mutex
	running bool
	cancel  context.CancelFunc
	cleanup func()

	BytesSent atomic.Uint64
	BytesRecv atomic.Uint64
}

func NewRelay() *Relay {
	return &Relay{}
}

func (r *Relay) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *Relay) Start(cfg Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.running {
		return fmt.Errorf("already running")
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.BytesSent.Store(0)
	r.BytesRecv.Store(0)

	var (
		cleanup func()
		err     error
	)

	mode := cfg.effectiveMode()
	transport := cfg.effectiveTransport()

	switch {
	case mode == "server":
		cleanup, err = r.startServer(ctx, cfg)
	case transport == "tcp":
		cleanup, err = r.startClientTCP(ctx, cfg)
	default:
		cleanup, err = r.startClientUDP(ctx, cfg)
	}

	if err != nil {
		cancel()
		return err
	}

	r.cancel = cancel
	r.cleanup = cleanup
	r.running = true
	return nil
}

func (r *Relay) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.running {
		return
	}
	r.cancel()
	if r.cleanup != nil {
		r.cleanup()
	}
	r.running = false
}

// --- Client UDP mode (original) ---

func (r *Relay) startClientUDP(ctx context.Context, cfg Config) (func(), error) {
	if cfg.RemoteAddr == "" {
		return nil, fmt.Errorf("remote_addr not configured")
	}

	listenAddr := &net.UDPAddr{IP: net.ParseIP(cfg.ListenAddr), Port: cfg.ListenPort}
	listener, err := net.ListenUDP("udp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}

	remoteAddr := &net.UDPAddr{IP: net.ParseIP(cfg.RemoteAddr), Port: cfg.RemotePort}

	go r.runClientUDP(ctx, listener, remoteAddr)

	return func() { listener.Close() }, nil
}

type udpSession struct {
	outConn  *net.UDPConn
	srcAddr  *net.UDPAddr
	lastSeen atomic.Int64
}

func (r *Relay) runClientUDP(ctx context.Context, listener *net.UDPConn, remoteAddr *net.UDPAddr) {
	var (
		sessions  = make(map[string]*udpSession)
		sessionMu sync.Mutex
	)

	// Cleanup stale sessions
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now().Unix()
				sessionMu.Lock()
				for key, s := range sessions {
					if now-s.lastSeen.Load() > 120 {
						s.outConn.Close()
						delete(sessions, key)
					}
				}
				sessionMu.Unlock()
			}
		}
	}()

	buf := make([]byte, 65536)
	for {
		n, srcAddr, err := listener.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				sessionMu.Lock()
				for _, s := range sessions {
					s.outConn.Close()
				}
				sessionMu.Unlock()
				return
			default:
				continue
			}
		}

		key := srcAddr.String()
		sessionMu.Lock()
		s, exists := sessions[key]
		if !exists {
			outConn, dialErr := net.DialUDP("udp", nil, remoteAddr)
			if dialErr != nil {
				sessionMu.Unlock()
				continue
			}
			s = &udpSession{outConn: outConn, srcAddr: srcAddr}
			sessions[key] = s

			go func(s *udpSession) {
				respBuf := make([]byte, 65536)
				for {
					rn, readErr := s.outConn.Read(respBuf)
					if readErr != nil {
						return
					}
					s.lastSeen.Store(time.Now().Unix())
					r.BytesRecv.Add(uint64(rn))
					listener.WriteToUDP(respBuf[:rn], s.srcAddr)
				}
			}(s)
		}
		sessionMu.Unlock()

		s.lastSeen.Store(time.Now().Unix())
		r.BytesSent.Add(uint64(n))
		s.outConn.Write(buf[:n])
	}
}

// --- Status helper for GUI ---

func (r *Relay) StatusLine(cfg Config) string {
	mode := cfg.effectiveMode()
	transport := cfg.effectiveTransport()

	if mode == "server" {
		return fmt.Sprintf("Serving %s:%d (%s) -> %s:%d",
			cfg.ListenAddr, cfg.ListenPort, transport,
			cfg.ForwardAddr, cfg.ForwardPort)
	}
	return fmt.Sprintf("%s:%d -> %s:%d (%s)",
		cfg.ListenAddr, cfg.ListenPort,
		cfg.RemoteAddr, cfg.RemotePort, transport)
}

func logRelay(cfg Config) {
	mode := cfg.effectiveMode()
	transport := cfg.effectiveTransport()
	if mode == "server" {
		log.Printf("Server listening on %s:%d (%s), forwarding to %s:%d (udp)",
			cfg.ListenAddr, cfg.ListenPort, transport,
			cfg.ForwardAddr, cfg.ForwardPort)
	} else {
		log.Printf("Client relaying %s:%d -> %s:%d (%s)",
			cfg.ListenAddr, cfg.ListenPort,
			cfg.RemoteAddr, cfg.RemotePort, transport)
	}
}
