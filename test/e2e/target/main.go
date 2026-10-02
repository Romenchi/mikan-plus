// target is the far end for node e2e tests. Its wire format makes the expected byte
// counts exact: a request is 1 op byte + 8-byte big-endian length.
//
//	'D' n: server sends n bytes, closes        → client up 9, down n
//	'U' n: client sends n bytes, server "K"    → client up 9+n, down 1
//	'S' r: server streams r bytes/s until the client goes away
//	'W' 0: server sends the address the connection came from, as text
//
// UDP on :9001 echoes every datagram.
package main

import (
	"encoding/binary"
	"io"
	"log"
	"net"
	"time"
)

func main() {
	go udpEcho()
	ln, err := net.Listen("tcp", ":9000")
	if err != nil {
		log.Fatal(err)
	}
	for {
		c, err := ln.Accept()
		if err != nil {
			log.Fatal(err)
		}
		go serve(c)
	}
}

func serve(c net.Conn) {
	defer c.Close()
	hdr := make([]byte, 9)
	if _, err := io.ReadFull(c, hdr); err != nil {
		return
	}
	n := int64(binary.BigEndian.Uint64(hdr[1:]))
	buf := make([]byte, 32<<10)
	for i := range buf {
		buf[i] = byte(i)
	}
	switch hdr[0] {
	case 'W':
		host, _, _ := net.SplitHostPort(c.RemoteAddr().String())
		_, _ = c.Write([]byte(host))
	case 'D':
		for n > 0 {
			k := min(n, int64(len(buf)))
			if _, err := c.Write(buf[:k]); err != nil {
				return
			}
			n -= k
		}
	case 'U':
		if _, err := io.CopyN(io.Discard, c, n); err != nil {
			return
		}
		_, _ = c.Write([]byte{'K'})
	case 'S':
		chunk := max(n/20, 1024)
		t := time.NewTicker(50 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			if _, err := c.Write(buf[:min(chunk, int64(len(buf)))]); err != nil {
				return
			}
		}
	}
}

func udpEcho() {
	pc, err := net.ListenPacket("udp", ":9001")
	if err != nil {
		log.Fatal(err)
	}
	buf := make([]byte, 65535)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(buf[:n], addr)
	}
}
