package socks5

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	socksVersion       = 0x05
	authMethodNone     = 0x00
	authMethodUserPass = 0x02
	authMethodRejected = 0xFF

	commandConnect      = 0x01
	commandUDPAssociate = 0x03

	addrTypeIPv4   = 0x01
	addrTypeDomain = 0x03
	addrTypeIPv6   = 0x04

	replySucceeded           = 0x00
	replyGeneralFailure      = 0x01
	replyCommandUnsupported  = 0x07
	replyAddrTypeUnsupported = 0x08
)

type AuthConfig struct {
	Username string
	Password string
}

type Config struct {
	ListenAddr         string
	PublicHost         string
	PublicPort         int
	AllowUDP           bool
	UpstreamSOCKS5Addr string
	Auth               *AuthConfig

	DialContext func(context.Context, string, string) (net.Conn, error)
	UDPTimeout  time.Duration
}

type Server struct {
	cfg Config

	tcpLn *net.TCPListener
	udpLn *net.UDPConn

	closeOnce sync.Once
	doneCh    chan struct{}

	mu           sync.RWMutex
	associations map[string]*association
}

type association struct {
	clientAddr *net.UDPAddr
	conn       net.Conn
}

func NewServer(cfg Config) *Server {
	return &Server{
		cfg:          cfg,
		doneCh:       make(chan struct{}),
		associations: make(map[string]*association),
	}
}

func (s *Server) Start(ctx context.Context) error {
	if s.cfg.ListenAddr == "" {
		return errors.New("listen address is required")
	}

	tcpAddr, err := net.ResolveTCPAddr("tcp", s.cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("resolve tcp listen addr: %w", err)
	}
	tcpLn, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		return fmt.Errorf("listen tcp: %w", err)
	}

	s.tcpLn = tcpLn
	if s.cfg.AllowUDP {
		udpListenIP := tcpLn.Addr().(*net.TCPAddr).IP
		if udpListenIP == nil {
			udpListenIP = net.ParseIP("0.0.0.0")
		}
		udpLn, err := net.ListenUDP("udp", &net.UDPAddr{
			IP:   udpListenIP,
			Port: tcpLn.Addr().(*net.TCPAddr).Port,
		})
		if err != nil {
			_ = tcpLn.Close()
			return fmt.Errorf("listen udp: %w", err)
		}
		s.udpLn = udpLn
	}

	go s.acceptLoop(ctx)
	if s.udpLn != nil {
		go s.udpLoop(ctx)
	}
	return nil
}

func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.doneCh)
		if s.tcpLn != nil {
			err = s.tcpLn.Close()
		}
		if s.udpLn != nil {
			if closeErr := s.udpLn.Close(); err == nil {
				err = closeErr
			}
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		for key, assoc := range s.associations {
			_ = assoc.conn.Close()
			delete(s.associations, key)
		}
	})
	return err
}

func (s *Server) TCPAddr() string {
	if s.tcpLn == nil {
		return ""
	}
	return s.tcpLn.Addr().String()
}

func (s *Server) acceptLoop(ctx context.Context) {
	for {
		conn, err := s.tcpLn.AcceptTCP()
		if err != nil {
			select {
			case <-s.doneCh:
				return
			default:
				return
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn *net.TCPConn) {
	defer conn.Close()

	if err := s.negotiateAuth(conn); err != nil {
		return
	}

	cmd, targetAddr, err := readRequest(conn)
	if err != nil {
		return
	}

	switch cmd {
	case commandConnect:
		s.handleConnect(ctx, conn, targetAddr)
	case commandUDPAssociate:
		if !s.cfg.AllowUDP || s.udpLn == nil {
			_ = writeReply(conn, replyCommandUnsupported, "0.0.0.0", 0)
			return
		}
		s.handleUDPAssociate(conn, targetAddr)
	default:
		_ = writeReply(conn, replyCommandUnsupported, "0.0.0.0", 0)
	}
}

func (s *Server) negotiateAuth(conn net.Conn) error {
	head, err := readExactly(conn, 2)
	if err != nil {
		return err
	}
	if head[0] != socksVersion {
		return fmt.Errorf("unsupported socks version %d", head[0])
	}
	methods, err := readExactly(conn, int(head[1]))
	if err != nil {
		return err
	}

	requiredMethod := byte(authMethodNone)
	if s.cfg.Auth != nil {
		requiredMethod = byte(authMethodUserPass)
	}
	if !containsMethod(methods, requiredMethod) {
		_, _ = conn.Write([]byte{socksVersion, authMethodRejected})
		return errors.New("no acceptable auth method")
	}
	if _, err := conn.Write([]byte{socksVersion, requiredMethod}); err != nil {
		return err
	}

	if requiredMethod != authMethodUserPass {
		return nil
	}

	if err := s.verifyUsernamePassword(conn); err != nil {
		return err
	}
	return nil
}

func (s *Server) verifyUsernamePassword(conn net.Conn) error {
	version, err := readExactly(conn, 1)
	if err != nil {
		return err
	}
	if version[0] != 0x01 {
		return fmt.Errorf("unsupported auth version %d", version[0])
	}
	userLen, err := readExactly(conn, 1)
	if err != nil {
		return err
	}
	user, err := readExactly(conn, int(userLen[0]))
	if err != nil {
		return err
	}
	passLen, err := readExactly(conn, 1)
	if err != nil {
		return err
	}
	pass, err := readExactly(conn, int(passLen[0]))
	if err != nil {
		return err
	}

	status := byte(0x00)
	if string(user) != s.cfg.Auth.Username || string(pass) != s.cfg.Auth.Password {
		status = 0x01
	}
	if _, err := conn.Write([]byte{0x01, status}); err != nil {
		return err
	}
	if status != 0x00 {
		return errors.New("invalid username or password")
	}
	return nil
}

func (s *Server) handleConnect(ctx context.Context, clientConn net.Conn, targetAddr string) {
	dialContext := s.cfg.DialContext
	if dialContext == nil {
		var dialer net.Dialer
		dialContext = dialer.DialContext
	}

	upstream, err := dialContext(ctx, "tcp", targetAddr)
	if err != nil {
		_ = writeReply(clientConn, replyGeneralFailure, "0.0.0.0", 0)
		return
	}
	defer upstream.Close()

	localHost, localPort := splitHostPort(upstream.LocalAddr().String())
	if err := writeReply(clientConn, replySucceeded, localHost, localPort); err != nil {
		return
	}

	copyDone := make(chan struct{}, 2)
	go proxyCopy(copyDone, upstream, clientConn)
	go proxyCopy(copyDone, clientConn, upstream)
	<-copyDone
}

func (s *Server) handleUDPAssociate(conn net.Conn, targetAddr string) {
	host, port := splitHostPort(targetAddr)
	clientAddr := &net.UDPAddr{Port: port}
	clientIP := net.ParseIP(host)
	if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
		if clientIP == nil || clientIP.IsUnspecified() || clientIP.IsLoopback() {
			clientIP = tcpAddr.IP
		}
	}
	if clientIP == nil {
		if tcpAddr, ok := conn.RemoteAddr().(*net.TCPAddr); ok {
			clientIP = tcpAddr.IP
		}
	}
	clientAddr.IP = clientIP

	assoc := &association{
		clientAddr: clientAddr,
		conn:       conn,
	}
	s.storeAssociation(clientAddr, assoc)
	defer s.deleteAssociation(clientAddr)

	if err := writeReply(conn, replySucceeded, s.publicHost(), s.publicPort()); err != nil {
		return
	}

	_, _ = io.Copy(io.Discard, conn)
}

func (s *Server) udpLoop(ctx context.Context) {
	buf := make([]byte, 64*1024)
	for {
		n, src, err := s.udpLn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.doneCh:
				return
			default:
				return
			}
		}
		packet := append([]byte(nil), buf[:n]...)
		go s.handleUDPPacket(ctx, src, packet)
	}
}

func (s *Server) handleUDPPacket(ctx context.Context, src *net.UDPAddr, packet []byte) {
	assoc := s.associationForSource(src)
	if assoc == nil {
		return
	}

	targetHost, targetPort, payload, err := parseUDPPacket(packet)
	if err != nil {
		log.Printf("socks5 udp: parse packet from %s failed: %v", src, err)
		return
	}
	targetAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(targetHost, strconv.Itoa(targetPort)))
	if err != nil {
		log.Printf("socks5 udp: resolve target %s:%d failed: %v", targetHost, targetPort, err)
		return
	}

	remoteConn, err := net.DialUDP("udp", nil, targetAddr)
	if err != nil {
		log.Printf("socks5 udp: dial target %s failed: %v", targetAddr, err)
		return
	}
	defer remoteConn.Close()

	timeout := s.cfg.UDPTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	_ = remoteConn.SetDeadline(time.Now().Add(timeout))
	if _, err := remoteConn.Write(payload); err != nil {
		log.Printf("socks5 udp: write target %s failed: %v", targetAddr, err)
		return
	}

	reply := make([]byte, 64*1024)
	n, _, err := remoteConn.ReadFromUDP(reply)
	if err != nil {
		log.Printf("socks5 udp: read target %s failed: %v", targetAddr, err)
		return
	}

	response, err := buildUDPReply(targetAddr.IP.String(), targetAddr.Port, reply[:n])
	if err != nil {
		log.Printf("socks5 udp: build reply for %s failed: %v", targetAddr, err)
		return
	}
	if _, err := s.udpLn.WriteToUDP(response, assoc.clientAddr); err != nil {
		log.Printf("socks5 udp: write reply to %s failed: %v", assoc.clientAddr, err)
		return
	}
}

func (s *Server) publicHost() string {
	if s.cfg.PublicHost != "" {
		return s.cfg.PublicHost
	}
	host, _ := splitHostPort(s.tcpLn.Addr().String())
	if host == "" || host == "0.0.0.0" || host == "::" {
		return "127.0.0.1"
	}
	return host
}

func (s *Server) publicPort() int {
	if s.cfg.PublicPort > 0 {
		return s.cfg.PublicPort
	}
	if s.udpLn != nil {
		return s.udpLn.LocalAddr().(*net.UDPAddr).Port
	}
	if s.tcpLn != nil {
		return s.tcpLn.Addr().(*net.TCPAddr).Port
	}
	return 0
}

func (s *Server) storeAssociation(addr *net.UDPAddr, assoc *association) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.associations[addr.String()] = assoc
}

func (s *Server) deleteAssociation(addr *net.UDPAddr) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.associations, addr.String())
}

func (s *Server) lookupAssociation(addr *net.UDPAddr) *association {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.associations[addr.String()]
}

func (s *Server) associationForSource(addr *net.UDPAddr) *association {
	if assoc := s.lookupAssociation(addr); assoc != nil {
		return assoc
	}
	if assoc := s.adoptAssociationMatchingSourcePort(addr); assoc != nil {
		return assoc
	}
	return s.adoptSoleAssociation(addr)
}

func (s *Server) adoptAssociationMatchingSourcePort(addr *net.UDPAddr) *association {
	s.mu.Lock()
	defer s.mu.Unlock()

	if assoc, ok := s.associations[addr.String()]; ok {
		return assoc
	}

	var matchedKey string
	var matchedAssoc *association
	for key, assoc := range s.associations {
		if assoc.clientAddr == nil || assoc.clientAddr.Port != addr.Port {
			continue
		}
		if assoc.clientAddr.IP != nil && !assoc.clientAddr.IP.IsLoopback() && !assoc.clientAddr.IP.IsUnspecified() && !assoc.clientAddr.IP.Equal(addr.IP) {
			continue
		}
		if matchedAssoc != nil {
			return nil
		}
		matchedKey = key
		matchedAssoc = assoc
	}

	if matchedAssoc == nil {
		return nil
	}

	return s.adoptAssociationLocked(matchedKey, matchedAssoc, addr)
}

func (s *Server) adoptSoleAssociation(addr *net.UDPAddr) *association {
	s.mu.Lock()
	defer s.mu.Unlock()

	if assoc, ok := s.associations[addr.String()]; ok {
		return assoc
	}

	if len(s.associations) != 1 {
		return nil
	}

	for key, assoc := range s.associations {
		return s.adoptAssociationLocked(key, assoc, addr)
	}

	return nil
}

func (s *Server) adoptAssociationLocked(key string, assoc *association, addr *net.UDPAddr) *association {
	delete(s.associations, key)
	assoc.clientAddr = cloneUDPAddr(addr)
	s.associations[assoc.clientAddr.String()] = assoc
	return assoc
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}

	cloned := &net.UDPAddr{
		Port: addr.Port,
		Zone: addr.Zone,
	}
	if addr.IP != nil {
		cloned.IP = append(net.IP(nil), addr.IP...)
	}
	return cloned
}

func proxyCopy(done chan<- struct{}, dst io.Writer, src io.Reader) {
	_, _ = io.Copy(dst, src)
	done <- struct{}{}
}

func readRequest(conn net.Conn) (byte, string, error) {
	head, err := readExactly(conn, 4)
	if err != nil {
		return 0, "", err
	}
	if head[0] != socksVersion {
		return 0, "", fmt.Errorf("unsupported socks version %d", head[0])
	}
	address, err := readAddress(conn, head[3])
	if err != nil {
		return 0, "", err
	}
	portBytes, err := readExactly(conn, 2)
	if err != nil {
		return 0, "", err
	}
	port := binary.BigEndian.Uint16(portBytes)
	return head[1], net.JoinHostPort(address, strconv.Itoa(int(port))), nil
}

func readAddress(conn net.Conn, atyp byte) (string, error) {
	switch atyp {
	case addrTypeIPv4:
		payload, err := readExactly(conn, 4)
		if err != nil {
			return "", err
		}
		return net.IP(payload).String(), nil
	case addrTypeIPv6:
		payload, err := readExactly(conn, 16)
		if err != nil {
			return "", err
		}
		return net.IP(payload).String(), nil
	case addrTypeDomain:
		size, err := readExactly(conn, 1)
		if err != nil {
			return "", err
		}
		payload, err := readExactly(conn, int(size[0]))
		if err != nil {
			return "", err
		}
		return string(payload), nil
	default:
		return "", fmt.Errorf("unsupported address type %d", atyp)
	}
}

func writeReply(conn net.Conn, code byte, host string, port int) error {
	addr := net.ParseIP(host)
	if addr == nil {
		host = "0.0.0.0"
		addr = net.ParseIP(host)
	}
	reply := []byte{socksVersion, code, 0x00}
	if ipv4 := addr.To4(); ipv4 != nil {
		reply = append(reply, addrTypeIPv4)
		reply = append(reply, ipv4...)
	} else if ipv6 := addr.To16(); ipv6 != nil {
		reply = append(reply, addrTypeIPv6)
		reply = append(reply, ipv6...)
	} else {
		return fmt.Errorf("invalid reply host %q", host)
	}
	reply = append(reply, 0x00, 0x00)
	binary.BigEndian.PutUint16(reply[len(reply)-2:], uint16(port))
	_, err := conn.Write(reply)
	return err
}

func parseUDPPacket(packet []byte) (string, int, []byte, error) {
	if len(packet) < 10 {
		return "", 0, nil, errors.New("udp packet too short")
	}
	if packet[0] != 0x00 || packet[1] != 0x00 || packet[2] != 0x00 {
		return "", 0, nil, errors.New("invalid udp header")
	}

	idx := 4
	var host string
	switch packet[3] {
	case addrTypeIPv4:
		host = net.IP(packet[idx : idx+4]).String()
		idx += 4
	case addrTypeIPv6:
		host = net.IP(packet[idx : idx+16]).String()
		idx += 16
	case addrTypeDomain:
		size := int(packet[idx])
		idx++
		host = string(packet[idx : idx+size])
		idx += size
	default:
		return "", 0, nil, fmt.Errorf("unsupported udp address type %d", packet[3])
	}
	port := int(binary.BigEndian.Uint16(packet[idx : idx+2]))
	idx += 2
	return host, port, packet[idx:], nil
}

func buildUDPReply(host string, port int, payload []byte) ([]byte, error) {
	addr := net.ParseIP(host)
	if addr == nil {
		return nil, fmt.Errorf("invalid udp reply host %q", host)
	}
	ipv4 := addr.To4()
	if ipv4 == nil {
		return nil, fmt.Errorf("only IPv4 udp replies are supported")
	}
	reply := []byte{0x00, 0x00, 0x00, addrTypeIPv4}
	reply = append(reply, ipv4...)
	reply = append(reply, 0x00, 0x00)
	binary.BigEndian.PutUint16(reply[len(reply)-2:], uint16(port))
	reply = append(reply, payload...)
	return reply, nil
}

func readExactly(conn net.Conn, size int) ([]byte, error) {
	buf := make([]byte, size)
	_, err := io.ReadFull(conn, buf)
	return buf, err
}

func containsMethod(methods []byte, want byte) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}

func splitHostPort(addr string) (string, int) {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return "0.0.0.0", 0
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return host, 0
	}
	return host, port
}
