package main

import (
	"bytes"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"
)

// helper: find a free UDP port
func freeUDPPort(t *testing.T) int {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := conn.LocalAddr().(*net.UDPAddr).Port
	conn.Close()
	return port
}

// helper: find a free TCP port
func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// echoUDPServer: receives UDP packets and echoes them back with "echo:" prefix
func echoUDPServer(t *testing.T, addr string) (port int, stop func()) {
	t.Helper()
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	port = conn.LocalAddr().(*net.UDPAddr).Port

	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 65536)
		for {
			n, remote, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			resp := append([]byte("echo:"), buf[:n]...)
			conn.WriteTo(resp, remote)
		}
	}()

	return port, func() {
		conn.Close()
		<-done
	}
}

// sendUDP: send data to addr and read response with timeout
func sendUDP(t *testing.T, addr string, data []byte) []byte {
	t.Helper()
	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(data); err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 65536)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return buf[:n]
}

// --- Tests ---

func TestClientUDP_E2E(t *testing.T) {
	// Echo server (simulates WireGuard on ROS)
	echoPort, stopEcho := echoUDPServer(t, "127.0.0.1:0")
	defer stopEcho()

	// Relay
	relayPort := freeUDPPort(t)
	relay := NewRelay()
	err := relay.Start(Config{
		Mode:       "client",
		Transport:  "udp",
		ListenAddr: "127.0.0.1",
		ListenPort: relayPort,
		RemoteAddr: "127.0.0.1",
		RemotePort: echoPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Stop()

	// Send through relay
	resp := sendUDP(t, fmt.Sprintf("127.0.0.1:%d", relayPort), []byte("hello"))
	if !bytes.Equal(resp, []byte("echo:hello")) {
		t.Fatalf("got %q, want %q", resp, "echo:hello")
	}

	// Verify stats
	if relay.BytesSent.Load() != 5 {
		t.Fatalf("BytesSent = %d, want 5", relay.BytesSent.Load())
	}
	if relay.BytesRecv.Load() != 10 { // "echo:hello" = 10 bytes
		t.Fatalf("BytesRecv = %d, want 10", relay.BytesRecv.Load())
	}
}

func TestClientUDP_MultipleSessions(t *testing.T) {
	echoPort, stopEcho := echoUDPServer(t, "127.0.0.1:0")
	defer stopEcho()

	relayPort := freeUDPPort(t)
	relay := NewRelay()
	err := relay.Start(Config{
		Mode:       "client",
		Transport:  "udp",
		ListenAddr: "127.0.0.1",
		ListenPort: relayPort,
		RemoteAddr: "127.0.0.1",
		RemotePort: echoPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Stop()

	// Send from multiple "clients" concurrently
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			msg := fmt.Sprintf("client%d", id)
			resp := sendUDP(t, fmt.Sprintf("127.0.0.1:%d", relayPort), []byte(msg))
			want := "echo:" + msg
			if string(resp) != want {
				t.Errorf("client %d: got %q, want %q", id, resp, want)
			}
		}(i)
	}
	wg.Wait()
}

func TestTCP_E2E(t *testing.T) {
	// Echo UDP server (simulates WireGuard on ROS)
	echoPort, stopEcho := echoUDPServer(t, "127.0.0.1:0")
	defer stopEcho()

	// TCP server relay (simulates pve)
	serverTCPPort := freeTCPPort(t)
	server := NewRelay()
	err := server.Start(Config{
		Mode:        "server",
		Transport:   "tcp",
		ListenAddr:  "127.0.0.1",
		ListenPort:  serverTCPPort,
		ForwardAddr: "127.0.0.1",
		ForwardPort: echoPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	// TCP client relay (simulates friend's machine)
	clientUDPPort := freeUDPPort(t)
	client := NewRelay()
	err = client.Start(Config{
		Mode:       "client",
		Transport:  "tcp",
		ListenAddr: "127.0.0.1",
		ListenPort: clientUDPPort,
		RemoteAddr: "127.0.0.1",
		RemotePort: serverTCPPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()

	// Give TCP connection a moment to establish
	time.Sleep(100 * time.Millisecond)

	// Send UDP through the full chain:
	// UDP client → client relay (UDP→TCP) → server relay (TCP→UDP) → echo server
	resp := sendUDP(t, fmt.Sprintf("127.0.0.1:%d", clientUDPPort), []byte("through-tcp"))
	if !bytes.Equal(resp, []byte("echo:through-tcp")) {
		t.Fatalf("got %q, want %q", resp, "echo:through-tcp")
	}
}

func TestTCP_MultiplePackets(t *testing.T) {
	echoPort, stopEcho := echoUDPServer(t, "127.0.0.1:0")
	defer stopEcho()

	serverPort := freeTCPPort(t)
	server := NewRelay()
	err := server.Start(Config{
		Mode: "server", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: serverPort,
		ForwardAddr: "127.0.0.1", ForwardPort: echoPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	clientPort := freeUDPPort(t)
	client := NewRelay()
	err = client.Start(Config{
		Mode: "client", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: clientPort,
		RemoteAddr: "127.0.0.1", RemotePort: serverPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()

	time.Sleep(100 * time.Millisecond)

	// Send multiple packets sequentially
	for i := 0; i < 20; i++ {
		msg := fmt.Sprintf("packet-%03d", i)
		resp := sendUDP(t, fmt.Sprintf("127.0.0.1:%d", clientPort), []byte(msg))
		want := "echo:" + msg
		if string(resp) != want {
			t.Fatalf("packet %d: got %q, want %q", i, resp, want)
		}
	}
}

func TestTCP_Reconnect(t *testing.T) {
	echoPort, stopEcho := echoUDPServer(t, "127.0.0.1:0")
	defer stopEcho()

	serverPort := freeTCPPort(t)

	// Start server
	server := NewRelay()
	err := server.Start(Config{
		Mode: "server", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: serverPort,
		ForwardAddr: "127.0.0.1", ForwardPort: echoPort,
	})
	if err != nil {
		t.Fatal(err)
	}

	clientPort := freeUDPPort(t)
	client := NewRelay()
	err = client.Start(Config{
		Mode: "client", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: clientPort,
		RemoteAddr: "127.0.0.1", RemotePort: serverPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Stop()

	time.Sleep(100 * time.Millisecond)

	// Verify it works
	resp := sendUDP(t, fmt.Sprintf("127.0.0.1:%d", clientPort), []byte("before"))
	if string(resp) != "echo:before" {
		t.Fatalf("before restart: got %q", resp)
	}

	// Kill and restart server (simulates network interruption)
	server.Stop()
	time.Sleep(200 * time.Millisecond)

	server2 := NewRelay()
	err = server2.Start(Config{
		Mode: "server", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: serverPort,
		ForwardAddr: "127.0.0.1", ForwardPort: echoPort,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer server2.Stop()

	// Client should reconnect on next packet
	// May need a couple attempts for reconnection
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		time.Sleep(200 * time.Millisecond)
		conn, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", clientPort))
		if err != nil {
			lastErr = err
			continue
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		conn.Write([]byte("after"))
		buf := make([]byte, 65536)
		n, err := conn.Read(buf)
		conn.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if string(buf[:n]) == "echo:after" {
			return // success
		}
		lastErr = fmt.Errorf("got %q", buf[:n])
	}
	t.Fatalf("reconnect failed after 5 attempts: %v", lastErr)
}

func TestLargePacket(t *testing.T) {
	echoPort, stopEcho := echoUDPServer(t, "127.0.0.1:0")
	defer stopEcho()

	serverPort := freeTCPPort(t)
	server := NewRelay()
	server.Start(Config{
		Mode: "server", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: serverPort,
		ForwardAddr: "127.0.0.1", ForwardPort: echoPort,
	})
	defer server.Stop()

	clientPort := freeUDPPort(t)
	client := NewRelay()
	client.Start(Config{
		Mode: "client", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: clientPort,
		RemoteAddr: "127.0.0.1", RemotePort: serverPort,
	})
	defer client.Stop()

	time.Sleep(100 * time.Millisecond)

	// Send a large packet (typical WG MTU ~1420)
	payload := bytes.Repeat([]byte{0x42}, 1400)
	resp := sendUDP(t, fmt.Sprintf("127.0.0.1:%d", clientPort), payload)
	want := append([]byte("echo:"), payload...)
	if !bytes.Equal(resp, want) {
		t.Fatalf("large packet: got %d bytes, want %d bytes", len(resp), len(want))
	}
}

func TestStartStop(t *testing.T) {
	relay := NewRelay()

	if relay.IsRunning() {
		t.Fatal("should not be running initially")
	}

	port := freeUDPPort(t)
	err := relay.Start(Config{
		Mode: "client", Transport: "udp",
		ListenAddr: "127.0.0.1", ListenPort: port,
		RemoteAddr: "127.0.0.1", RemotePort: 1234,
	})
	if err != nil {
		t.Fatal(err)
	}

	if !relay.IsRunning() {
		t.Fatal("should be running after Start")
	}

	// Double start should error
	err = relay.Start(Config{
		ListenAddr: "127.0.0.1", ListenPort: port,
		RemoteAddr: "127.0.0.1", RemotePort: 1234,
	})
	if err == nil {
		t.Fatal("double start should error")
	}

	relay.Stop()

	if relay.IsRunning() {
		t.Fatal("should not be running after Stop")
	}

	// Double stop should not panic
	relay.Stop()
}

func TestStartValidation(t *testing.T) {
	relay := NewRelay()

	err := relay.Start(Config{
		Mode: "client", Transport: "udp",
		ListenAddr: "127.0.0.1", ListenPort: freeUDPPort(t),
		RemoteAddr: "", // missing
	})
	if err == nil {
		t.Fatal("should error on missing remote_addr")
	}

	err = relay.Start(Config{
		Mode: "server", Transport: "tcp",
		ListenAddr: "127.0.0.1", ListenPort: freeTCPPort(t),
		ForwardAddr: "", // missing
	})
	if err == nil {
		t.Fatal("should error on missing forward_addr")
	}
}
