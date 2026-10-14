// Package terminal runs interactive shells for the web terminal: a pseudo
// terminal made with the kernel's /dev/ptmx, carried over a WebSocket
// (RFC 6455) implemented here with the standard library.
package terminal

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// Conn is a server-side WebSocket connection.
type Conn struct {
	c   net.Conn
	r   *bufio.Reader
	wmu sync.Mutex
}

// AcceptKey computes Sec-WebSocket-Accept.
func AcceptKey(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

func headerHas(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, t := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(t), token) {
				return true
			}
		}
	}
	return false
}

// IsUpgrade reports whether r asks for a WebSocket.
func IsUpgrade(r *http.Request) bool {
	return headerHas(r.Header, "Connection", "upgrade") && headerHas(r.Header, "Upgrade", "websocket")
}

// Upgrade completes the handshake and takes over the connection.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if r.Method != http.MethodGet || !IsUpgrade(r) {
		http.Error(w, "expected a WebSocket", http.StatusBadRequest)
		return nil, errors.New("not a WebSocket request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		w.Header().Set("Sec-WebSocket-Version", "13")
		http.Error(w, "unsupported WebSocket version", http.StatusUpgradeRequired)
		return nil, errors.New("bad WebSocket version")
	}
	key := r.Header.Get("Sec-WebSocket-Key")
	if k, err := base64.StdEncoding.DecodeString(key); err != nil || len(k) != 16 {
		http.Error(w, "bad Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("bad key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("connection can't be taken over")
	}
	c, brw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + AcceptKey(key) + "\r\n\r\n"
	if _, err := c.Write([]byte(resp)); err != nil {
		c.Close()
		return nil, err
	}
	_ = c.SetDeadline(time.Time{})
	return &Conn{c: c, r: brw.Reader}, nil
}

// ReadMessage returns the next data message, answering pings itself.
func (ws *Conn) ReadMessage() (op byte, data []byte, err error) {
	var msg []byte
	var msgOp byte
	for {
		fin, opcode, payload, err := ws.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch opcode {
		case OpPing:
			_ = ws.write(OpPong, payload)
			continue
		case OpPong:
			continue
		case OpClose:
			_ = ws.write(OpClose, payload)
			return OpClose, payload, io.EOF
		case OpContinuation:
			if msg == nil && msgOp == 0 {
				return 0, nil, errors.New("unexpected continuation frame")
			}
		default:
			msgOp = opcode
		}
		msg = append(msg, payload...)
		if len(msg) > 1<<20 {
			return 0, nil, errors.New("message too large")
		}
		if fin {
			return msgOp, msg, nil
		}
	}
}

func (ws *Conn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(ws.r, h[:]); err != nil {
		return
	}
	fin = h[0]&0x80 != 0
	if h[0]&0x70 != 0 {
		return false, 0, nil, errors.New("reserved bits set")
	}
	op = h[0] & 0x0f
	masked := h[1]&0x80 != 0
	n := uint64(h[1] & 0x7f)
	switch n {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(ws.r, b[:]); err != nil {
			return
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(ws.r, b[:]); err != nil {
			return
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if !masked {
		return false, 0, nil, errors.New("client frames must be masked")
	}
	if n > 1<<20 {
		return false, 0, nil, errors.New("frame too large")
	}
	var mask [4]byte
	if _, err = io.ReadFull(ws.r, mask[:]); err != nil {
		return
	}
	payload = make([]byte, n)
	if _, err = io.ReadFull(ws.r, payload); err != nil {
		return
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return fin, op, payload, nil
}

func (ws *Conn) write(op byte, data []byte) error {
	ws.wmu.Lock()
	defer ws.wmu.Unlock()
	hdr := []byte{0x80 | op}
	switch n := len(data); {
	case n < 126:
		hdr = append(hdr, byte(n))
	case n < 65536:
		hdr = append(hdr, 126, byte(n>>8), byte(n))
	default:
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(n))
		hdr = append(append(hdr, 127), b[:]...)
	}
	_ = ws.c.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := ws.c.Write(append(hdr, data...)); err != nil {
		return err
	}
	return nil
}

// WriteBinary sends terminal output.
func (ws *Conn) WriteBinary(b []byte) error { return ws.write(OpBinary, b) }

// WriteText sends a control message.
func (ws *Conn) WriteText(s string) error { return ws.write(OpText, []byte(s)) }

// Close sends a close frame with a reason and closes the connection.
func (ws *Conn) Close(code uint16, reason string) error {
	b := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(b, code)
	_ = ws.write(OpClose, append(b, reason...))
	return ws.c.Close()
}

// SetReadDeadline limits idle time.
func (ws *Conn) SetReadDeadline(t time.Time) error { return ws.c.SetReadDeadline(t) }

var _ = fmt.Sprint
