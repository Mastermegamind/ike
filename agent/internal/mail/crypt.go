package mail

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"strconv"
	"strings"
)

// SHA-512 crypt ("$6$"), the scheme Dovecot calls SHA512-CRYPT and glibc's
// crypt(3) implements. Written here because the standard library has no
// crypt and Ike takes no outside dependencies.

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

func sha512Crypt(password, salt string, rounds int) string {
	if len(salt) > 16 {
		salt = salt[:16]
	}
	custom := rounds != 5000
	if rounds < 1000 {
		rounds = 1000
	}
	if rounds > 999999999 {
		rounds = 999999999
	}
	p, s := []byte(password), []byte(salt)

	b := sha512.New()
	b.Write(p)
	b.Write(s)
	b.Write(p)
	sumB := b.Sum(nil)

	a := sha512.New()
	a.Write(p)
	a.Write(s)
	for i := len(p); i > 0; i -= 64 {
		if i > 64 {
			a.Write(sumB)
		} else {
			a.Write(sumB[:i])
		}
	}
	for i := len(p); i > 0; i >>= 1 {
		if i&1 != 0 {
			a.Write(sumB)
		} else {
			a.Write(p)
		}
	}
	sumA := a.Sum(nil)

	dp := sha512.New()
	for range p {
		dp.Write(p)
	}
	sumDP := dp.Sum(nil)
	P := make([]byte, 0, len(p))
	for i := len(p); i > 0; i -= 64 {
		if i > 64 {
			P = append(P, sumDP...)
		} else {
			P = append(P, sumDP[:i]...)
		}
	}

	ds := sha512.New()
	for i := 0; i < 16+int(sumA[0]); i++ {
		ds.Write(s)
	}
	sumDS := ds.Sum(nil)
	S := make([]byte, 0, len(s))
	for i := len(s); i > 0; i -= 64 {
		if i > 64 {
			S = append(S, sumDS...)
		} else {
			S = append(S, sumDS[:i]...)
		}
	}

	c := sumA
	for i := 0; i < rounds; i++ {
		h := sha512.New()
		if i&1 != 0 {
			h.Write(P)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(S)
		}
		if i%7 != 0 {
			h.Write(P)
		}
		if i&1 != 0 {
			h.Write(c)
		} else {
			h.Write(P)
		}
		c = h.Sum(nil)
	}

	var out strings.Builder
	out.WriteString("$6$")
	if custom {
		out.WriteString("rounds=" + strconv.Itoa(rounds) + "$")
	}
	out.WriteString(salt)
	out.WriteByte('$')
	enc := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for ; n > 0; n-- {
			out.WriteByte(cryptAlphabet[w&0x3f])
			w >>= 6
		}
	}
	order := [][3]int{{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48}, {28, 49, 7},
		{50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13}, {56, 14, 35}, {15, 36, 57},
		{37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41}}
	for _, o := range order {
		enc(c[o[0]], c[o[1]], c[o[2]], 4)
	}
	enc(0, 0, c[63], 2)
	return out.String()
}

// HashPassword returns a Dovecot password field: {SHA512-CRYPT}$6$...
func HashPassword(pw string) string {
	salt := make([]byte, 16)
	rb := make([]byte, 16)
	_, _ = rand.Read(rb)
	for i := range salt {
		salt[i] = cryptAlphabet[int(rb[i])%len(cryptAlphabet)]
	}
	return "{SHA512-CRYPT}" + sha512Crypt(pw, string(salt), 5000)
}

// CheckPassword verifies a password against a stored {SHA512-CRYPT} hash.
func CheckPassword(stored, pw string) bool {
	h := strings.TrimPrefix(strings.TrimPrefix(stored, "{SHA512-CRYPT}"), "{CRYPT}")
	if !strings.HasPrefix(h, "$6$") {
		return false
	}
	parts := strings.Split(h[3:], "$")
	rounds := 5000
	if len(parts) == 3 && strings.HasPrefix(parts[0], "rounds=") {
		rounds, _ = strconv.Atoi(strings.TrimPrefix(parts[0], "rounds="))
		parts = parts[1:]
	}
	if len(parts) != 2 {
		return false
	}
	got := sha512Crypt(pw, parts[0], rounds)
	return subtle.ConstantTimeCompare([]byte(got), []byte(h)) == 1
}
