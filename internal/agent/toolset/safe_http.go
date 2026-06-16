package toolset

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

// blockedAddrError is returned by the dial Control hook when a connection
// target resolves to a disallowed (private/loopback/link-local) address.
// It is a distinct type so callers can recognize the failure via errors.As
// and explain it precisely instead of surfacing a raw dial error.
type blockedAddrError struct{ ip net.IP }

func (e *blockedAddrError) Error() string {
	return fmt.Sprintf("address %s is in a blocked (private/loopback/link-local) range", e.ip)
}

// isBlockedFetchIP reports whether ip is in a range we refuse to fetch from by
// default (SSRF protection): loopback, link-local (which includes the cloud
// metadata endpoint 169.254.169.254), private, unique-local, unspecified, or
// multicast.
func isBlockedFetchIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() ||
		ip.IsPrivate()
}

// safeFetchHTTPClient returns an HTTP client whose dialer rejects connections
// to private/internal addresses unless allowPrivate is set. The check runs on
// the post-DNS-resolution IP inside the dial Control hook, so it also defeats
// DNS rebinding to an internal address (the actual dialed IP is inspected, not
// the hostname).
func safeFetchHTTPClient(timeout time.Duration, allowPrivate bool) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			_ = network
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil {
				return fmt.Errorf("cannot parse dial address %q", address)
			}
			if isBlockedFetchIP(ip) {
				return &blockedAddrError{ip: ip}
			}
			return nil
		}
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			ResponseHeaderTimeout: timeout,
			Proxy:                 http.ProxyFromEnvironment,
		},
	}
}
