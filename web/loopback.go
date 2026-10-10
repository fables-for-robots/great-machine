package web

import (
	"net"
	"net/netip"
	"strings"
)

// IsLoopback says whether addr, as --web-addr has it (host:port), names
// a loopback address: an IP that is one, or localhost. An empty host
// (":8080") is every address and is not loopback.
//
// The handler asks this of nothing: it answers under every host name.
// It is for the command, which warns at start about an address that is
// not loopback.
func IsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && ip.IsLoopback()
}
