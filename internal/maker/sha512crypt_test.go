package maker

import (
	"strings"
	"testing"
)

// Expected values from `openssl passwd -6 -salt SALT PASSWORD` on Debian;
// the first is also the reference vector in the SHA-crypt specification.
func TestSHA512Crypt(t *testing.T) {
	for _, c := range []struct{ pw, salt, want string }{
		{"Hello world!", "saltstring", "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"pa$$ wörd", "Ab3/xyZ", "$6$Ab3/xyZ$kmXyO4cJ00Zjyh7BsT.l4lmHqdV8ggn.e.YEErErW1XiBRMgfrqDB8/RZOdmW9UHoOxwRP/oY4aInTLhk0ADt."},
		{"this is a much longer password that is more than sixty-four bytes long!!", "longsalt1234567", "$6$longsalt1234567$8wX6.DQcbcbJrPD2cVHr.u2r7RIJZ7jiJbwnbcwSbgY//TwO/li4ArqGdpqbdtCWIPMKtcPMATaA3B2AS103S/"},
	} {
		if got := sha512Crypt(c.pw, c.salt); got != c.want {
			t.Errorf("%q: got %s", c.pw, got)
		}
	}
	h := HashPassword("x")
	if !strings.HasPrefix(h, "$6$") || len(strings.Split(h, "$")[2]) != 16 {
		t.Errorf("HashPassword = %s", h)
	}
}
