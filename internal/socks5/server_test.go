package socks5

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestServer_ConnectRelaysTCPPayload(t *testing.T) {
	echoLn, echoAddr := startTCPEchoServer(t)

	server := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		AllowUDP:   true,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start socks5 server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = echoLn.Close() })

	conn := dialSOCKS5(t, server.TCPAddr(), nil)
	defer conn.Close()

	host, portText, err := net.SplitHostPort(echoAddr)
	if err != nil {
		t.Fatalf("split echo addr: %v", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatalf("parse echo port: %v", err)
	}
	sendConnect(t, conn, host, port)

	payload := []byte("hello over socks5")
	if _, err := conn.Write(payload); err != nil {
		t.Fatalf("write proxied payload: %v", err)
	}

	reply := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, reply); err != nil {
		t.Fatalf("read proxied payload: %v", err)
	}
	if string(reply) != strings.ToUpper(string(payload)) {
		t.Fatalf("unexpected connect relay reply: got %q want %q", string(reply), strings.ToUpper(string(payload)))
	}
}

func TestServer_UsernamePasswordAuthRequired(t *testing.T) {
	server := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		Auth: &AuthConfig{
			Username: "demo",
			Password: "strong-pass",
		},
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start socks5 server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	noAuthConn, err := net.DialTimeout("tcp", server.TCPAddr(), 5*time.Second)
	if err != nil {
		t.Fatalf("dial socks5 server: %v", err)
	}
	defer noAuthConn.Close()
	if _, err := noAuthConn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write no-auth greeting: %v", err)
	}
	if got := mustReadBytes(t, noAuthConn, 2); got[1] != 0xFF {
		t.Fatalf("expected no acceptable auth methods, got %v", got)
	}

	authConn := dialSOCKS5(t, server.TCPAddr(), &AuthConfig{
		Username: "demo",
		Password: "strong-pass",
	})
	defer authConn.Close()
}

func TestServer_UDPAssociateRelaysDatagramAndAdvertisesConfiguredHost(t *testing.T) {
	udpConn, udpAddr := startUDPEchoServer(t)

	server := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		AllowUDP:   true,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start socks5 server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = udpConn.Close() })

	controlConn := dialSOCKS5(t, server.TCPAddr(), nil)
	defer controlConn.Close()

	clientUDP, relayHost, relayPort := sendUDPAssociate(t, controlConn)
	defer clientUDP.Close()

	if relayHost != "127.0.0.1" {
		t.Fatalf("expected relay host 127.0.0.1, got %q", relayHost)
	}

	targetHost, targetPortText, err := net.SplitHostPort(udpAddr)
	if err != nil {
		t.Fatalf("split udp echo addr: %v", err)
	}
	targetPort, err := strconv.Atoi(targetPortText)
	if err != nil {
		t.Fatalf("parse udp echo port: %v", err)
	}

	payload := []byte("udp over socks5")
	packet := buildUDPRequest(t, targetHost, targetPort, payload)
	if _, err := clientUDP.WriteToUDP(packet, &net.UDPAddr{IP: net.ParseIP(relayHost), Port: relayPort}); err != nil {
		t.Fatalf("write udp associate payload: %v", err)
	}

	buf := make([]byte, 2048)
	if err := clientUDP.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set udp deadline: %v", err)
	}
	n, _, err := clientUDP.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read udp associate reply: %v", err)
	}

	gotHost, gotPort, gotPayload := parseUDPReply(t, buf[:n])
	if gotHost != targetHost || gotPort != targetPort {
		t.Fatalf("unexpected udp reply source: got %s:%d want %s:%d", gotHost, gotPort, targetHost, targetPort)
	}
	if string(gotPayload) != strings.ToUpper(string(payload)) {
		t.Fatalf("unexpected udp relay payload: got %q want %q", string(gotPayload), strings.ToUpper(string(payload)))
	}
}

func TestServer_UDPAssociateAdvertisesConfiguredPublicPort(t *testing.T) {
	server := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		PublicPort: 18080,
		AllowUDP:   true,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start socks5 server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	controlConn := dialSOCKS5(t, server.TCPAddr(), nil)
	defer controlConn.Close()

	clientUDP, relayHost, relayPort := sendUDPAssociate(t, controlConn)
	defer clientUDP.Close()

	if relayHost != "127.0.0.1" {
		t.Fatalf("expected relay host 127.0.0.1, got %q", relayHost)
	}
	if relayPort != 18080 {
		t.Fatalf("expected relay port 18080, got %d", relayPort)
	}
}

func TestServer_UDPAssociateRejectedWhenDisabled(t *testing.T) {
	server := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		AllowUDP:   false,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start socks5 server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	controlConn := dialSOCKS5(t, server.TCPAddr(), nil)
	defer controlConn.Close()

	clientUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen local udp client: %v", err)
	}
	defer clientUDP.Close()
	localAddr := clientUDP.LocalAddr().(*net.UDPAddr)

	req := []byte{0x05, 0x03, 0x00, 0x01}
	req = append(req, localAddr.IP.To4()...)
	req = append(req, 0x00, 0x00)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(localAddr.Port))
	if _, err := controlConn.Write(req); err != nil {
		t.Fatalf("write udp associate request: %v", err)
	}

	reply := mustReadBytes(t, controlConn, 10)
	if reply[0] != 0x05 || reply[1] != 0x07 {
		t.Fatalf("expected udp associate to be rejected, got %v", reply)
	}
}

func TestServer_UDPAssociateUsesTCPPeerWhenClientAdvertisesUnspecifiedAddress(t *testing.T) {
	udpConn, udpAddr := startUDPEchoServer(t)

	server := NewServer(Config{
		ListenAddr: "127.0.0.1:0",
		PublicHost: "127.0.0.1",
		AllowUDP:   true,
	})
	if err := server.Start(context.Background()); err != nil {
		t.Fatalf("start socks5 server: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	t.Cleanup(func() { _ = udpConn.Close() })

	controlConn := dialSOCKS5(t, server.TCPAddr(), nil)
	defer controlConn.Close()

	clientUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen local udp client: %v", err)
	}
	defer clientUDP.Close()

	req := []byte{0x05, 0x03, 0x00, 0x01}
	req = append(req, []byte{0x00, 0x00, 0x00, 0x00}...)
	req = append(req, 0x00, 0x00)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(clientUDP.LocalAddr().(*net.UDPAddr).Port))
	if _, err := controlConn.Write(req); err != nil {
		t.Fatalf("write udp associate request: %v", err)
	}

	replyHead := mustReadBytes(t, controlConn, 4)
	if replyHead[0] != 0x05 || replyHead[1] != 0x00 {
		t.Fatalf("unexpected udp associate reply head: %v", replyHead)
	}
	if replyHead[3] != 0x01 {
		t.Fatalf("expected IPv4 udp associate reply, got atyp=%d", replyHead[3])
	}
	relayHost := net.IP(mustReadBytes(t, controlConn, 4)).String()
	relayPort := int(binary.BigEndian.Uint16(mustReadBytes(t, controlConn, 2)))

	targetHost, targetPortText, err := net.SplitHostPort(udpAddr)
	if err != nil {
		t.Fatalf("split udp echo addr: %v", err)
	}
	targetPort, err := strconv.Atoi(targetPortText)
	if err != nil {
		t.Fatalf("parse udp echo port: %v", err)
	}

	payload := []byte("udp over socks5 via unspecified")
	packet := buildUDPRequest(t, targetHost, targetPort, payload)
	if _, err := clientUDP.WriteToUDP(packet, &net.UDPAddr{IP: net.ParseIP(relayHost), Port: relayPort}); err != nil {
		t.Fatalf("write udp associate payload: %v", err)
	}

	buf := make([]byte, 2048)
	if err := clientUDP.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set udp deadline: %v", err)
	}
	n, _, err := clientUDP.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("read udp associate reply: %v", err)
	}

	gotHost, gotPort, gotPayload := parseUDPReply(t, buf[:n])
	if gotHost != targetHost || gotPort != targetPort {
		t.Fatalf("unexpected udp reply source: got %s:%d want %s:%d", gotHost, gotPort, targetHost, targetPort)
	}
	if string(gotPayload) != strings.ToUpper(string(payload)) {
		t.Fatalf("unexpected udp relay payload: got %q want %q", string(gotPayload), strings.ToUpper(string(payload)))
	}
}

func TestWriteReply_SupportsIPv6BindAddress(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()

	writeErrCh := make(chan error, 1)
	go func() {
		writeErrCh <- writeReply(serverConn, replySucceeded, "2001:db8::1", 4242)
		_ = serverConn.Close()
	}()

	reply := mustReadBytes(t, clientConn, 22)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("unexpected connect reply head: %v", reply[:4])
	}
	if reply[3] != 0x04 {
		t.Fatalf("expected IPv6 bind reply, got atyp=%d", reply[3])
	}

	if got := net.IP(reply[4:20]).String(); got != "2001:db8::1" {
		t.Fatalf("unexpected IPv6 bind host: got %q want %q", got, "2001:db8::1")
	}

	if got := int(binary.BigEndian.Uint16(reply[20:22])); got != 4242 {
		t.Fatalf("unexpected IPv6 bind port: got %d want %d", got, 4242)
	}

	if err := <-writeErrCh; err != nil {
		t.Fatalf("write IPv6 reply: %v", err)
	}
}

func TestServer_AssociationForSourceAdoptsObservedSourceWhenStoredClientIPIsLoopback(t *testing.T) {
	server := NewServer(Config{})
	server.storeAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000}, &association{
		clientAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000},
	})

	got := server.associationForSource(&net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 53000})
	if got == nil {
		t.Fatal("expected association to adopt observed udp source")
	}
	if got.clientAddr == nil || !got.clientAddr.IP.Equal(net.ParseIP("192.0.2.10")) || got.clientAddr.Port != 53000 {
		t.Fatalf("expected adopted client address 192.0.2.10:53000, got %#v", got.clientAddr)
	}
}

func TestServer_AssociationForSourceAdoptsObservedSourcePortWhenDockerRemapsUDPPort(t *testing.T) {
	server := NewServer(Config{})
	server.storeAssociation(&net.UDPAddr{IP: net.ParseIP("192.168.215.1"), Port: 54467}, &association{
		clientAddr: &net.UDPAddr{IP: net.ParseIP("192.168.215.1"), Port: 54467},
	})

	got := server.associationForSource(&net.UDPAddr{IP: net.ParseIP("192.168.215.1"), Port: 26574})
	if got == nil {
		t.Fatal("expected association to adopt docker-remapped udp source port")
	}
	if got.clientAddr == nil || !got.clientAddr.IP.Equal(net.ParseIP("192.168.215.1")) || got.clientAddr.Port != 26574 {
		t.Fatalf("expected adopted client address 192.168.215.1:26574, got %#v", got.clientAddr)
	}
}

func TestServer_AssociationForSourceAdoptsSoleAssociationWhenNATChangesSource(t *testing.T) {
	server := NewServer(Config{})
	server.storeAssociation(&net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000}, &association{
		clientAddr: &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 53000},
	})

	got := server.associationForSource(&net.UDPAddr{IP: net.ParseIP("192.0.2.10"), Port: 54000})
	if got == nil {
		t.Fatal("expected sole association to be adopted for first udp packet")
	}
	if got.clientAddr == nil || !got.clientAddr.IP.Equal(net.ParseIP("192.0.2.10")) || got.clientAddr.Port != 54000 {
		t.Fatalf("expected adopted client address 192.0.2.10:54000, got %#v", got.clientAddr)
	}
}

func startTCPEchoServer(t *testing.T) (net.Listener, string) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen tcp echo: %v", err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 2048)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						_, _ = c.Write([]byte(strings.ToUpper(string(buf[:n]))))
					}
					if err != nil {
						return
					}
				}
			}(conn)
		}
	}()
	return ln, ln.Addr().String()
}

func startUDPEchoServer(t *testing.T) (*net.UDPConn, string) {
	t.Helper()

	addr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("resolve udp echo addr: %v", err)
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		t.Fatalf("listen udp echo: %v", err)
	}
	go func() {
		buf := make([]byte, 2048)
		for {
			n, peer, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			_, _ = conn.WriteToUDP([]byte(strings.ToUpper(string(buf[:n]))), peer)
		}
	}()
	return conn, conn.LocalAddr().String()
}

func dialSOCKS5(t *testing.T, address string, auth *AuthConfig) net.Conn {
	t.Helper()

	conn, err := net.DialTimeout("tcp", address, 5*time.Second)
	if err != nil {
		t.Fatalf("dial socks5 server: %v", err)
	}

	if auth == nil {
		if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
			t.Fatalf("write no-auth greeting: %v", err)
		}
		reply := mustReadBytes(t, conn, 2)
		if reply[0] != 0x05 || reply[1] != 0x00 {
			t.Fatalf("unexpected no-auth greeting reply: %v", reply)
		}
		return conn
	}

	if _, err := conn.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatalf("write auth greeting: %v", err)
	}
	reply := mustReadBytes(t, conn, 2)
	if reply[0] != 0x05 || reply[1] != 0x02 {
		t.Fatalf("unexpected auth greeting reply: %v", reply)
	}
	user := []byte(auth.Username)
	pass := []byte(auth.Password)
	request := append([]byte{0x01, byte(len(user))}, user...)
	request = append(request, byte(len(pass)))
	request = append(request, pass...)
	if _, err := conn.Write(request); err != nil {
		t.Fatalf("write auth request: %v", err)
	}
	authReply := mustReadBytes(t, conn, 2)
	if authReply[0] != 0x01 || authReply[1] != 0x00 {
		t.Fatalf("unexpected auth reply: %v", authReply)
	}
	return conn
}

func sendConnect(t *testing.T, conn net.Conn, host string, port int) {
	t.Helper()

	req := []byte{0x05, 0x01, 0x00, 0x01}
	req = append(req, net.ParseIP(host).To4()...)
	req = append(req, 0x00, 0x00)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(port))

	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write connect request: %v", err)
	}
	reply := mustReadBytes(t, conn, 10)
	if reply[0] != 0x05 || reply[1] != 0x00 {
		t.Fatalf("unexpected connect reply: %v", reply)
	}
}

func sendUDPAssociate(t *testing.T, conn net.Conn) (*net.UDPConn, string, int) {
	t.Helper()

	clientUDP, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen local udp client: %v", err)
	}
	localAddr := clientUDP.LocalAddr().(*net.UDPAddr)

	req := []byte{0x05, 0x03, 0x00, 0x01}
	req = append(req, localAddr.IP.To4()...)
	req = append(req, 0x00, 0x00)
	binary.BigEndian.PutUint16(req[len(req)-2:], uint16(localAddr.Port))
	if _, err := conn.Write(req); err != nil {
		t.Fatalf("write udp associate request: %v", err)
	}

	replyHead := mustReadBytes(t, conn, 4)
	if replyHead[0] != 0x05 || replyHead[1] != 0x00 {
		t.Fatalf("unexpected udp associate reply head: %v", replyHead)
	}
	if replyHead[3] != 0x01 {
		t.Fatalf("expected IPv4 udp associate reply, got atyp=%d", replyHead[3])
	}
	addr := net.IP(mustReadBytes(t, conn, 4)).String()
	portBytes := mustReadBytes(t, conn, 2)
	port := int(binary.BigEndian.Uint16(portBytes))
	return clientUDP, addr, port
}

func buildUDPRequest(t *testing.T, host string, port int, payload []byte) []byte {
	t.Helper()

	packet := []byte{0x00, 0x00, 0x00, 0x01}
	packet = append(packet, net.ParseIP(host).To4()...)
	packet = append(packet, 0x00, 0x00)
	binary.BigEndian.PutUint16(packet[len(packet)-2:], uint16(port))
	packet = append(packet, payload...)
	return packet
}

func parseUDPReply(t *testing.T, packet []byte) (string, int, []byte) {
	t.Helper()

	if len(packet) < 10 {
		t.Fatalf("udp reply too short: %d", len(packet))
	}
	if packet[0] != 0x00 || packet[1] != 0x00 || packet[2] != 0x00 {
		t.Fatalf("invalid udp reply header: %v", packet[:3])
	}
	if packet[3] != 0x01 {
		t.Fatalf("expected IPv4 udp reply, got atyp=%d", packet[3])
	}
	host := net.IP(packet[4:8]).String()
	port := int(binary.BigEndian.Uint16(packet[8:10]))
	return host, port, packet[10:]
}

func mustReadBytes(t *testing.T, conn net.Conn, size int) []byte {
	t.Helper()

	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatalf("read %d bytes: %v", size, err)
	}
	return buf
}
