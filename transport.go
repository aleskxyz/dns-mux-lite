package main

import (
	"fmt"
	"net"
	"time"
)

const (
	dnsBufferSize   = 4096
	upstreamTimeout = 5 * time.Second
)

func sendQueryUDP(data []byte, addr string, timeout time.Duration) ([]byte, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}
	buf := make([]byte, dnsBufferSize)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	if n < 12 {
		return nil, fmt.Errorf("short DNS response")
	}
	return buf[:n], nil
}

