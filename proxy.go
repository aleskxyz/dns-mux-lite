package main

import (
	"log/slog"
	"net"
	"sync/atomic"
	"time"
)

const maxWorkers = 256

// UDPProxy listens for DNS queries over UDP and forwards to the resolver pool.
type UDPProxy struct {
	listenAddr string
	pool       *ResolverPool
	conn       *net.UDPConn
	queryCount uint64
	done       chan struct{}
}

func NewUDPProxy(addr string, pool *ResolverPool) *UDPProxy {
	return &UDPProxy{
		listenAddr: addr,
		pool:       pool,
		done:       make(chan struct{}),
	}
}

func (u *UDPProxy) Start() error {
	conn, err := u.bindWithRetry()
	if err != nil {
		return err
	}
	u.conn = conn
	slog.Info("UDP proxy listening", "addr", u.listenAddr)

	sem := make(chan struct{}, maxWorkers)
	buf := make([]byte, dnsBufferSize)

	for {
		select {
		case <-u.done:
			return nil
		default:
		}

		u.conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		n, clientAddr, err := u.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			select {
			case <-u.done:
				return nil
			default:
			}
			slog.Error("UDP socket error, rebinding...", "err", err)
			u.conn.Close()
			time.Sleep(3 * time.Second)
			conn, err := u.bindWithRetry()
			if err != nil {
				return err
			}
			u.conn = conn
			slog.Info("UDP socket rebound successfully")
			continue
		}

		data := make([]byte, n)
		copy(data, buf[:n])
		atomic.AddUint64(&u.queryCount, 1)

		conn := u.conn
		sem <- struct{}{}
		go func(data []byte, addr *net.UDPAddr) {
			defer func() { <-sem }()
			u.forward(conn, data, addr)
		}(data, clientAddr)
	}
}

func (u *UDPProxy) forward(conn *net.UDPConn, data []byte, clientAddr *net.UDPAddr) {
	resolver := u.pool.GetNext()
	u.pool.MarkSent(resolver)

	resp, err := u.pool.SendQuery(data, resolver)
	if err != nil {
		u.pool.MarkFailure(resolver)
		slog.Debug("Forward failed", "resolver", resolver, "err", err)

		// Retry with a different resolver
		retry := u.pool.GetNext()
		if retry != resolver {
			u.pool.MarkSent(retry)
			resp, err = u.pool.SendQuery(data, retry)
			if err != nil {
				u.pool.MarkFailure(retry)
				return
			}
			u.pool.MarkSuccess(retry)
		} else {
			return
		}
	} else {
		u.pool.MarkSuccess(resolver)
	}

	conn.WriteToUDP(resp, clientAddr)
}

func (u *UDPProxy) bindWithRetry() (*net.UDPConn, error) {
	addr, err := net.ResolveUDPAddr("udp", u.listenAddr)
	if err != nil {
		return nil, err
	}
	for {
		select {
		case <-u.done:
			return nil, net.ErrClosed
		default:
		}
		conn, err := net.ListenUDP("udp", addr)
		if err != nil {
			slog.Warn("UDP bind failed, retrying in 3s...", "addr", u.listenAddr, "err", err)
			time.Sleep(3 * time.Second)
			continue
		}
		return conn, nil
	}
}

func (u *UDPProxy) Stop() {
	close(u.done)
	if u.conn != nil {
		u.conn.Close()
	}
}

