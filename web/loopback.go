package web

import (
	"net"
	"net/netip"
	"strings"
)

// IsLoopback says whether addr, as --addr of `great-machine web` has it
// (host:port), names a loopback address: an IP that is one, or localhost.
// An empty host (":8080") is every address and is not loopback.
//
// It is for the command, which serves the page on no other address: the
// page acts with the key of the operator.
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
