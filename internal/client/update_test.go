package client

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{{"1.10.0", "1.9.2", true}, {"1.9.0", "1.9.0", false}, {"1.9.0", "1.10.0", false}, {"2.0.0", "1.99.99", true}, {"1.9.1", "dev", true}}
	for _, c := range cases {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s,%s) != %v", c.a, c.b, c.want)
		}
	}
}
