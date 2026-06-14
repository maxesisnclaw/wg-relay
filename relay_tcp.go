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

// --- Client TCP mode ---

func (r *Relay) startClientTCP(ctx context.Context, cfg Config) (func(), error) {
	if cfg.RemoteAddr == "" {
		return nil, fmt.Errorf("remote_addr not configured")
	}

	listenAddr := &net.UDPAddr{IP: net.ParseIP(cfg.ListenAddr), Port: cfg.ListenPort}
	listener, err := net.ListenUDP("udp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}

	go r.runClientTCP(ctx, listener, cfg)

	return func() { listener.Close() }, nil
}

func (r *Relay) runClientTCP(ctx context.Context, udpListener *net.UDPConn, cfg Config) {
	remoteAddr := net.JoinHostPort(cfg.RemoteAddr, fmt.Sprintf("%d", cfg.RemotePort))

	var (
		tcpConn      net.Conn
		tcpMu        sync.Mutex
		clientAddr   atomic.Pointer[net.UDPAddr]
		lastDialTime time.Time       // reconnect cooldown
		dialCooldown = 5 * time.Second
	)

	dial := func() (net.Conn, error) {
		tcpMu.Lock()
		defer tcpMu.Unlock()

		// Cooldown: don't spam reconnects
		if time.Since(lastDialTime) < dialCooldown {
			return nil, fmt.Errorf("cooldown (retry in %v)", dialCooldown-time.Since(lastDialTime))
		}
		lastDialTime = time.Now()

		if tcpConn != nil {
			tcpConn.Close()
			tcpConn = nil
		}
		log.Printf("Connecting to %s ...", remoteAddr)
		conn, err := net.DialTimeout("tcp", remoteAddr, 10*time.Second)
		if err != nil {
			return nil, err
		}
		log.Printf("Connected to %s", remoteAddr)
		if tc, ok := conn.(*net.TCPConn); ok {
			tc.SetKeepAlive(true)
			tc.SetKeepAlivePeriod(30 * time.Second)
		}
		tcpConn = conn

		// Read responses from TCP and forward back as UDP
		go func(c net.Conn) {
			for {
				data, err := readFrame(c)
				if err != nil {
					tcpMu.Lock()
					if tcpConn == c {
						tcpConn = nil
					}
					tcpMu.Unlock()
					c.Close()
					log.Printf("TCP connection lost, will reconnect on next packet")
					return
				}
				r.BytesRecv.Add(uint64(len(data)))
				if ca := clientAddr.Load(); ca != nil {
					udpListener.WriteToUDP(data, ca)
				}
			}
		}(conn)

		return conn, nil
	}

	// Try initial connection (non-fatal if fails)
	if _, err := dial(); err != nil {
		log.Printf("Initial TCP connect failed (will retry): %v", err)
	}

	buf := make([]byte, 65536)
	for {
		n, srcAddr, err := udpListener.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-ctx.Done():
				tcpMu.Lock()
				if tcpConn != nil {
					tcpConn.Close()
				}
				tcpMu.Unlock()
				return
			default:
				continue
			}
		}

		clientAddr.Store(srcAddr)

		tcpMu.Lock()
		conn := tcpConn
		tcpMu.Unlock()

		if conn == nil {
			var dialErr error
			conn, dialErr = dial()
			if dialErr != nil {
				continue // cooldown or dial error, drop packet silently
			}
		}

		r.BytesSent.Add(uint64(n))
		if err := writeFrame(conn, buf[:n]); err != nil {
			log.Printf("TCP write failed, will reconnect: %v", err)
			tcpMu.Lock()
			if tcpConn == conn {
				tcpConn.Close()
				tcpConn = nil
			}
			tcpMu.Unlock()
		}
	}
}

// --- Server TCP mode ---

func (r *Relay) startServer(ctx context.Context, cfg Config) (func(), error) {
	if cfg.ForwardAddr == "" {
		return nil, fmt.Errorf("forward_addr not configured")
	}

	listenAddr := net.JoinHostPort(cfg.ListenAddr, fmt.Sprintf("%d", cfg.ListenPort))
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen tcp: %w", err)
	}

	go r.runServer(ctx, listener, cfg)

	return func() { listener.Close() }, nil
}

func (r *Relay) runServer(ctx context.Context, tcpListener net.Listener, cfg Config) {
	forwardAddr := &net.UDPAddr{
		IP:   net.ParseIP(cfg.ForwardAddr),
		Port: cfg.ForwardPort,
	}

	go func() {
		<-ctx.Done()
		tcpListener.Close()
	}()

	for {
		conn, err := tcpListener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("TCP accept error: %v", err)
				continue
			}
		}
		log.Printf("Client connected: %s", conn.RemoteAddr())
		go r.handleServerConn(ctx, conn, forwardAddr)
	}
}

const serverReadTimeout = 60 * time.Second // WG keepalive is 25s, 60s = safe margin

func (r *Relay) handleServerConn(ctx context.Context, tcpConn net.Conn, forwardAddr *net.UDPAddr) {
	defer tcpConn.Close()

	// Enable TCP keepalive
	if tc, ok := tcpConn.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(30 * time.Second)
	}

	udpConn, err := net.DialUDP("udp", nil, forwardAddr)
	if err != nil {
		log.Printf("UDP dial to forward target failed: %v", err)
		return
	}
	defer udpConn.Close()

	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// UDP responses → TCP frames
	go func() {
		defer cancel()
		buf := make([]byte, 65536)
		for {
			n, err := udpConn.Read(buf)
			if err != nil {
				return
			}
			r.BytesRecv.Add(uint64(n))
			if err := writeFrame(tcpConn, buf[:n]); err != nil {
				return
			}
		}
	}()

	// TCP frames → UDP packets
	for {
		select {
		case <-connCtx.Done():
			return
		default:
		}

		// Read deadline: detect silently dead connections
		// WG sends keepalive every 25s, so 60s without data = dead
		tcpConn.SetReadDeadline(time.Now().Add(serverReadTimeout))
		data, err := readFrame(tcpConn)
		if err != nil {
			log.Printf("Client disconnected: %s (%v)", tcpConn.RemoteAddr(), err)
			return
		}
		r.BytesSent.Add(uint64(len(data)))
		udpConn.Write(data)
	}
}
