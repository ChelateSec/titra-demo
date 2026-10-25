// Package dnslog is a tiny logging DNS forwarder. The kernel trace sees
// connections only as IP addresses; this records which name each address came
// from, so the report can say "python3 → attacker.example" instead of an IP.
// It forwards every query unchanged and never blocks one.
package dnslog

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// Entry is one logged lookup.
type Entry struct {
	Time  time.Time `json:"time"`
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	Rcode string    `json:"rcode"`
	IPs   []string  `json:"ips,omitempty"`
	CNAME []string  `json:"cname,omitempty"`
}

// Server forwards DNS over UDP and TCP and logs each answer.
type Server struct {
	Listen   string
	Upstream string // host:port
	LogPath  string

	mu  sync.Mutex
	log *os.File
}

// Run serves until a listener fails. ready is called once both are bound.
func (s *Server) Run(ready func()) error {
	if _, _, err := net.SplitHostPort(s.Upstream); err != nil {
		s.Upstream = net.JoinHostPort(s.Upstream, "53")
	}
	f, err := os.OpenFile(s.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	s.log = f
	pc, err := net.ListenPacket("udp", s.Listen)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	if ready != nil {
		ready()
	}
	errc := make(chan error, 2)
	go func() { errc <- s.serveUDP(pc) }()
	go func() { errc <- s.serveTCP(ln) }()
	return <-errc
}

func (s *Server) serveUDP(pc net.PacketConn) error {
	for {
		buf := make([]byte, 4096)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		go func(q []byte, addr net.Addr) {
			up, err := net.DialTimeout("udp", s.Upstream, 3*time.Second)
			if err != nil {
				return
			}
			defer up.Close()
			_ = up.SetDeadline(time.Now().Add(5 * time.Second))
			if _, err := up.Write(q); err != nil {
				return
			}
			resp := make([]byte, 65535)
			m, err := up.Read(resp)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(resp[:m], addr)
			s.record(resp[:m])
		}(buf[:n], addr)
	}
}

func (s *Server) serveTCP(ln net.Listener) error {
	for {
		c, err := ln.Accept()
		if err != nil {
			return err
		}
		go func(c net.Conn) {
			defer c.Close()
			_ = c.SetDeadline(time.Now().Add(10 * time.Second))
			up, err := net.DialTimeout("tcp", s.Upstream, 3*time.Second)
			if err != nil {
				return
			}
			defer up.Close()
			_ = up.SetDeadline(time.Now().Add(10 * time.Second))
			r := bufio.NewReader(c)
			ur := bufio.NewReader(up)
			for {
				q, err := readTCPMsg(r)
				if err != nil {
					return
				}
				if err := writeTCPMsg(up, q); err != nil {
					return
				}
				resp, err := readTCPMsg(ur)
				if err != nil {
					return
				}
				if err := writeTCPMsg(c, resp); err != nil {
					return
				}
				s.record(resp)
			}
		}(c)
	}
}

func readTCPMsg(r io.Reader) ([]byte, error) {
	var l [2]byte
	if _, err := io.ReadFull(r, l[:]); err != nil {
		return nil, err
	}
	b := make([]byte, binary.BigEndian.Uint16(l[:]))
	_, err := io.ReadFull(r, b)
	return b, err
}

func writeTCPMsg(w io.Writer, b []byte) error {
	var l [2]byte
	binary.BigEndian.PutUint16(l[:], uint16(len(b)))
	_, err := w.Write(append(l[:], b...))
	return err
}

func (s *Server) record(resp []byte) {
	e, err := ParseResponse(resp)
	if err != nil || e.Name == "" {
		return
	}
	e.Time = time.Now().UTC()
	b, _ := json.Marshal(e)
	s.mu.Lock()
	_, _ = s.log.Write(append(b, '\n'))
	s.mu.Unlock()
}

var rcodes = map[int]string{0: "NOERROR", 1: "FORMERR", 2: "SERVFAIL", 3: "NXDOMAIN", 4: "NOTIMP", 5: "REFUSED"}
var qtypes = map[uint16]string{1: "A", 5: "CNAME", 28: "AAAA", 65: "HTTPS", 16: "TXT", 15: "MX", 12: "PTR", 33: "SRV"}

// ParseResponse extracts the question and A/AAAA/CNAME answers.
func ParseResponse(m []byte) (Entry, error) {
	var e Entry
	if len(m) < 12 {
		return e, errors.New("short")
	}
	rc := int(m[3] & 0x0f)
	e.Rcode = rcodes[rc]
	if e.Rcode == "" {
		e.Rcode = fmt.Sprintf("RCODE%d", rc)
	}
	qd := int(binary.BigEndian.Uint16(m[4:6]))
	an := int(binary.BigEndian.Uint16(m[6:8]))
	off := 12
	for i := 0; i < qd; i++ {
		name, n, err := readName(m, off)
		if err != nil || n+4 > len(m) {
			return e, errors.New("bad question")
		}
		t := binary.BigEndian.Uint16(m[n : n+2])
		if i == 0 {
			e.Name = strings.ToLower(strings.TrimSuffix(name, "."))
			e.Type = qtypes[t]
			if e.Type == "" {
				e.Type = "TYPE"
			}
		}
		off = n + 4
	}
	for i := 0; i < an; i++ {
		_, n, err := readName(m, off)
		if err != nil || n+10 > len(m) {
			break
		}
		t := binary.BigEndian.Uint16(m[n : n+2])
		rdlen := int(binary.BigEndian.Uint16(m[n+8 : n+10]))
		rd := n + 10
		if rd+rdlen > len(m) {
			break
		}
		switch t {
		case 1:
			if rdlen == 4 {
				e.IPs = append(e.IPs, net.IP(m[rd:rd+4]).String())
			}
		case 28:
			if rdlen == 16 {
				e.IPs = append(e.IPs, net.IP(m[rd:rd+16]).String())
			}
		case 5:
			if c, _, err := readName(m, rd); err == nil {
				e.CNAME = append(e.CNAME, strings.TrimSuffix(strings.ToLower(c), "."))
			}
		}
		off = rd + rdlen
	}
	return e, nil
}

// readName decodes a possibly-compressed name at off; returns the offset just
// past the name in the original position.
func readName(m []byte, off int) (string, int, error) {
	var labels []string
	end := -1
	for hops := 0; hops < 64; hops++ {
		if off >= len(m) {
			return "", 0, errors.New("name overflow")
		}
		l := int(m[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return strings.Join(labels, ".") + ".", end, nil
		case l&0xc0 == 0xc0:
			if off+1 >= len(m) {
				return "", 0, errors.New("bad pointer")
			}
			if end < 0 {
				end = off + 2
			}
			off = int(binary.BigEndian.Uint16(m[off:off+2]) & 0x3fff)
		default:
			if off+1+l > len(m) {
				return "", 0, errors.New("label overflow")
			}
			labels = append(labels, string(m[off+1:off+1+l]))
			off += 1 + l
		}
	}
	return "", 0, errors.New("too many pointers")
}

// ReadLog loads every entry from a dns.jsonl log.
func ReadLog(path string) []Entry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}
