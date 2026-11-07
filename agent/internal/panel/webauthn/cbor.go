// Package webauthn checks passkey registrations and sign-ins (W3C Web
// Authentication, level 2) with the standard library only: a small CBOR
// reader, COSE keys for ES256 and RS256, and the checks the specification
// lists for a relying party.
package webauthn

import (
	"errors"
	"fmt"
	"math"
)

// CBOR (RFC 8949), only what authenticators send: integers, byte and text
// strings, arrays, maps, true/false/null and floats. Indefinite lengths and
// tags are refused; nothing in WebAuthn uses them.

const (
	maxDepth = 16
	maxItems = 1 << 12
)

var errCBOR = errors.New("malformed CBOR")

type decoder struct {
	b   []byte
	pos int
}

// decodeCBOR reads one item from b and returns it with the bytes it used.
// Maps become map[any]any with int64 or string keys; byte strings []byte.
func decodeCBOR(b []byte) (any, int, error) {
	d := &decoder{b: b}
	v, err := d.item(0)
	return v, d.pos, err
}

func (d *decoder) need(n uint64) error {
	if n > uint64(len(d.b)-d.pos) {
		return errCBOR
	}
	return nil
}

func (d *decoder) head() (major byte, arg uint64, err error) {
	if err = d.need(1); err != nil {
		return
	}
	ib := d.b[d.pos]
	d.pos++
	major, info := ib>>5, ib&0x1f
	switch {
	case info < 24:
		return major, uint64(info), nil
	case info <= 27:
		n := uint64(1) << (info - 24)
		if err = d.need(n); err != nil {
			return
		}
		for i := uint64(0); i < n; i++ {
			arg = arg<<8 | uint64(d.b[d.pos])
			d.pos++
		}
		return major, arg, nil
	default:
		return 0, 0, fmt.Errorf("%w: unsupported length", errCBOR)
	}
}

func (d *decoder) item(depth int) (any, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("%w: nested too deeply", errCBOR)
	}
	start := d.pos
	major, arg, err := d.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0:
		if arg > math.MaxInt64 {
			return nil, errCBOR
		}
		return int64(arg), nil
	case 1:
		if arg > math.MaxInt64 {
			return nil, errCBOR
		}
		return -1 - int64(arg), nil
	case 2, 3:
		if err := d.need(arg); err != nil {
			return nil, err
		}
		v := d.b[d.pos : d.pos+int(arg)]
		d.pos += int(arg)
		if major == 3 {
			return string(v), nil
		}
		return append([]byte(nil), v...), nil
	case 4:
		if arg > maxItems {
			return nil, errCBOR
		}
		out := make([]any, 0, arg)
		for i := uint64(0); i < arg; i++ {
			v, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case 5:
		if arg > maxItems {
			return nil, errCBOR
		}
		out := make(map[any]any, arg)
		for i := uint64(0); i < arg; i++ {
			k, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			switch k.(type) {
			case int64, string:
			default:
				return nil, fmt.Errorf("%w: map key", errCBOR)
			}
			if _, dup := out[k]; dup {
				return nil, fmt.Errorf("%w: duplicate map key", errCBOR)
			}
			v, err := d.item(depth + 1)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case 7:
		info := d.b[start] & 0x1f
		switch {
		case info == 20:
			return false, nil
		case info == 21:
			return true, nil
		case info == 22 || info == 23:
			return nil, nil
		case info == 25:
			return float64(halfFloat(uint16(arg))), nil
		case info == 26:
			return float64(math.Float32frombits(uint32(arg))), nil
		case info == 27:
			return math.Float64frombits(arg), nil
		}
		return nil, fmt.Errorf("%w: simple value", errCBOR)
	}
	return nil, fmt.Errorf("%w: tags aren't supported", errCBOR)
}

func halfFloat(h uint16) float32 {
	sign := uint32(h>>15) << 31
	exp := uint32(h>>10) & 0x1f
	frac := uint32(h & 0x3ff)
	switch exp {
	case 0:
		f := float32(frac) / 1024 / 16384
		if sign != 0 {
			return -f
		}
		return f
	case 31:
		return math.Float32frombits(sign | 0x7f800000 | frac<<13)
	}
	return math.Float32frombits(sign | (exp+112)<<23 | frac<<13)
}
