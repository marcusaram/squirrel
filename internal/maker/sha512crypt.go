package maker

import (
	"crypto/rand"
	"crypto/sha512"
	"strings"
)

const cryptAlphabet = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// HashPassword returns a SHA-512 crypt ("$6$") hash, the format
// /etc/shadow and cloud-init's passwd field expect.
func HashPassword(password string) string {
	b := make([]byte, 16)
	rand.Read(b)
	salt := make([]byte, 16)
	for i := range salt {
		salt[i] = cryptAlphabet[int(b[i])%len(cryptAlphabet)]
	}
	return sha512Crypt(password, string(salt))
}

// sha512Crypt implements Ulrich Drepper's SHA-crypt with the default 5000
// rounds.
func sha512Crypt(password, salt string) string {
	const rounds = 5000
	if len(salt) > 16 {
		salt = salt[:16]
	}
	p, s := []byte(password), []byte(salt)

	h := sha512.New()
	h.Write(p)
	h.Write(s)
	h.Write(p)
	b := h.Sum(nil)

	h.Reset()
	h.Write(p)
	h.Write(s)
	for n := len(p); n > 0; n -= 64 {
		h.Write(b[:min(n, 64)])
	}
	for n := len(p); n > 0; n >>= 1 {
		if n&1 != 0 {
			h.Write(b)
		} else {
			h.Write(p)
		}
	}
	a := h.Sum(nil)

	h.Reset()
	for range len(p) {
		h.Write(p)
	}
	dp := h.Sum(nil)
	pBytes := repeatTo(dp, len(p))

	h.Reset()
	for range 16 + int(a[0]) {
		h.Write(s)
	}
	ds := h.Sum(nil)
	sBytes := repeatTo(ds, len(s))

	c := a
	for i := range rounds {
		h.Reset()
		if i%2 != 0 {
			h.Write(pBytes)
		} else {
			h.Write(c)
		}
		if i%3 != 0 {
			h.Write(sBytes)
		}
		if i%7 != 0 {
			h.Write(pBytes)
		}
		if i%2 != 0 {
			h.Write(c)
		} else {
			h.Write(pBytes)
		}
		c = h.Sum(nil)
	}

	var out strings.Builder
	out.WriteString("$6$" + salt + "$")
	enc := func(b2, b1, b0 byte, n int) {
		w := uint(b2)<<16 | uint(b1)<<8 | uint(b0)
		for range n {
			out.WriteByte(cryptAlphabet[w&0x3f])
			w >>= 6
		}
	}
	order := [][3]int{
		{0, 21, 42}, {22, 43, 1}, {44, 2, 23}, {3, 24, 45}, {25, 46, 4}, {47, 5, 26}, {6, 27, 48},
		{28, 49, 7}, {50, 8, 29}, {9, 30, 51}, {31, 52, 10}, {53, 11, 32}, {12, 33, 54}, {34, 55, 13},
		{56, 14, 35}, {15, 36, 57}, {37, 58, 16}, {59, 17, 38}, {18, 39, 60}, {40, 61, 19}, {62, 20, 41},
	}
	for _, o := range order {
		enc(c[o[0]], c[o[1]], c[o[2]], 4)
	}
	enc(0, 0, c[63], 2)
	return out.String()
}

func repeatTo(src []byte, n int) []byte {
	out := make([]byte, 0, n)
	for len(out) < n {
		out = append(out, src[:min(len(src), n-len(out))]...)
	}
	return out
}
