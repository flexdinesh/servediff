package browser

import (
	"net"
	"slices"
	"strconv"
)

// URLs returns a local browser URL followed by reachable interface URLs for a
// wildcard listener. Interface discovery failure leaves the local URL usable.
func URLs(host string, port int, path string) []string {
	var addresses []net.Addr
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		addresses, _ = net.InterfaceAddrs()
	}
	return urls(host, port, path, addresses)
}

func urls(host string, port int, path string, addresses []net.Addr) []string {
	bindIP := net.ParseIP(host)
	if host == "" || host == "127.0.0.1" || (bindIP != nil && bindIP.IsUnspecified()) {
		host = "localhost"
		if bindIP != nil && bindIP.To4() == nil {
			host = "::1"
		}
	}
	url := func(host string) string {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + path
	}
	result := []string{url(host)}
	if bindIP == nil || !bindIP.IsUnspecified() {
		return result
	}
	var lanURLs []string
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err != nil || !ip.IsGlobalUnicast() || (bindIP.To4() != nil && ip.To4() == nil) {
			continue
		}
		lanURLs = append(lanURLs, url(ip.String()))
	}
	slices.Sort(lanURLs)
	return append(result, slices.Compact(lanURLs)...)
}
