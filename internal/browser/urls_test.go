package browser

import (
	"net"
	"reflect"
	"testing"
)

func TestURLsRespectListenerAndAddressFamily(t *testing.T) {
	var addresses []net.Addr
	for _, raw := range []string{
		"192.168.1.20/24", "10.0.0.5/8", "192.168.1.20/32",
		"127.0.0.1/8", "169.254.1.2/16", "0.0.0.0/0", "224.0.0.1/4",
		"::1/128", "fe80::1/64", "fd00::5/64", "2001:db8::5/64",
	} {
		ip, network, err := net.ParseCIDR(raw)
		if err != nil {
			t.Fatal(err)
		}
		network.IP = ip
		addresses = append(addresses, network)
	}
	for _, test := range []struct {
		host string
		want []string
	}{
		{"", []string{"http://localhost:8123/contexts/observation"}},
		{"127.0.0.1", []string{"http://localhost:8123/contexts/observation"}},
		{"192.168.1.20", []string{"http://192.168.1.20:8123/contexts/observation"}},
		{"::1", []string{"http://[::1]:8123/contexts/observation"}},
		{"fd00::5", []string{"http://[fd00::5]:8123/contexts/observation"}},
		{"0.0.0.0", []string{
			"http://localhost:8123/contexts/observation",
			"http://10.0.0.5:8123/contexts/observation",
			"http://192.168.1.20:8123/contexts/observation",
		}},
		{"::", []string{
			"http://[::1]:8123/contexts/observation",
			"http://10.0.0.5:8123/contexts/observation",
			"http://192.168.1.20:8123/contexts/observation",
			"http://[2001:db8::5]:8123/contexts/observation",
			"http://[fd00::5]:8123/contexts/observation",
		}},
	} {
		t.Run(test.host, func(t *testing.T) {
			got := urls(test.host, 8123, "/contexts/observation", addresses)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("URLs = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWildcardURLsWithoutLANAddresses(t *testing.T) {
	for _, host := range []string{"0.0.0.0", "::"} {
		got := urls(host, 8123, "/contexts/observation", nil)
		if len(got) != 1 {
			t.Fatalf("missing local fallback: %v", got)
		}
	}
}
