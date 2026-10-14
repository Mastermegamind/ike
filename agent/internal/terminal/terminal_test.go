package terminal

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// clientFrame builds a masked client frame.
func clientFrame(op byte, data []byte) []byte {
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	h := []byte{0x80 | op}
	n := len(data)
	switch {
	case n < 126:
		h = append(h, 0x80|byte(n))
	default:
		h = append(h, 0x80|126, byte(n>>8), byte(n))
	}
	h = append(h, mask[:]...)
	for i, b := range data {
		h = append(h, b^mask[i%4])
	}
	return h
}

func readServerFrame(r *bufio.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	n := int(h[1] & 0x7f)
	if n == 126 {
		var b [2]byte
		_, _ = io.ReadFull(r, b[:])
		n = int(binary.BigEndian.Uint16(b[:]))
	}
	d := make([]byte, n)
	_, err := io.ReadFull(r, d)
	return h[0] & 0xf, d, err
}

func TestTerminalOverWebSocket(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := Upgrade(w, r)
		if err != nil {
			return
		}
		cmd := exec.Command("/bin/sh", "-c", `stty size; printf 'name? '; read x; echo "hello $x"`)
		_ = Serve(context.Background(), ws, cmd, 100, 30)
	}))
	defer srv.Close()
	c, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	key := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	_, _ = c.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: " + key + "\r\n\r\n"))
	r := bufio.NewReader(c)
	resp, err := http.ReadResponse(r, nil)
	if err != nil || resp.StatusCode != 101 || resp.Header.Get("Sec-WebSocket-Accept") != AcceptKey(key) {
		t.Fatalf("handshake: %v %+v", err, resp)
	}
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	var out strings.Builder
	sent := false
	for {
		op, data, err := readServerFrame(r)
		if err != nil {
			break
		}
		if op == OpClose {
			break
		}
		out.Write(data)
		if !sent && strings.Contains(out.String(), "name? ") {
			_, _ = c.Write(clientFrame(OpText, []byte(`{"type":"resize","cols":80,"rows":24}`)))
			_, _ = c.Write(clientFrame(OpPing, []byte("p")))
			_, _ = c.Write(clientFrame(OpBinary, []byte("ada\r")))
			sent = true
		}
	}
	s := out.String()
	if !strings.Contains(s, "30 100") || !strings.Contains(s, "hello ada") {
		t.Fatalf("terminal output: %q", s)
	}
}
