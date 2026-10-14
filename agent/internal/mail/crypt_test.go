package mail

import "testing"

func TestSHA512Crypt(t *testing.T) {
	// Test vectors from Ulrich Drepper's SHA-crypt specification.
	cases := []struct {
		pw, salt string
		rounds   int
		want     string
	}{
		{"Hello world!", "saltstring", 5000, "$6$saltstring$svn8UoSVapNtMuq1ukKS4tPQd8iKwSMHWjl/O817G3uBnIFNjnQJuesI68u4OTLiBFdcbYEdFCoEOfaS35inz1"},
		{"Hello world!", "saltstringsaltstring", 10000, "$6$rounds=10000$saltstringsaltst$OW1/O6BYHV6BcXZu8QVeXbDWra3Oeqh0sbHbbMCVNSnCM/UrjmM0Dp8vOuZeHBy/YTBmSK6H9qs/y3RnOaw5v."},
		{"a very much longer text to encrypt.  This one even stretches over morethan one line.", "anotherlongsaltstring", 1400,
			"$6$rounds=1400$anotherlongsalts$POfYwTEok97VWcjxIiSOjiykti.o/pQs.wPvMxQ6Fm7I6IoYN3CmLs66x9t0oSwbtEW7o7UmJEiDwGqd8p4ur1"},
	}
	for _, c := range cases {
		if got := sha512Crypt(c.pw, c.salt, c.rounds); got != c.want {
			t.Errorf("%q: got %s", c.pw, got)
		}
	}
	h := HashPassword("correct horse")
	if !CheckPassword(h, "correct horse") || CheckPassword(h, "wrong") {
		t.Fatal("round trip failed")
	}
}
