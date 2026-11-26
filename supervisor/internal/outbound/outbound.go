// Package outbound reaches the model providers and MCP servers of projects
// from the Supervisor, which asks them for their models and tools. It does
// not connect to an address of the Supervisor's own host or of its link, so
// that the URL that a project registers cannot reach into the Supervisor or
// the metadata of its node.
package outbound

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"syscall"
	"time"
)

// ErrRefused wraps the error of a connection to an address that is refused.
var ErrRefused = errors.New("the address is not reached from the Supervisor")

// Client returns an HTTP client that refuses such addresses and does not
// follow redirects. localhost, if not empty, is the host that it reaches in
// place of the local host of a URL.
func Client(localhost string) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: refuse}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = dialer.DialContext
	transport.Proxy = nil
	return Through(localhost, transport)
}

// Through returns a client as Client does, which sends its requests with the
// transport: the transport decides what is reached.
func Through(localhost string, transport http.RoundTripper) *http.Client {
	return &http.Client{
		Transport:     &substitute{localhost: localhost, next: transport},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// refuse refuses a connection to a loopback, link-local, unspecified or
// multicast address. It runs after the name is resolved.
func refuse(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	ip = ip.Unmap()
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast() {
		return fmt.Errorf("%w: %s", ErrRefused, ip)
	}
	return nil
}

// substitute sends a request for the local host to the host that stands in
// for it.
type substitute struct {
	localhost string
	next      http.RoundTripper
}

func (s *substitute) RoundTrip(request *http.Request) (*http.Response, error) {
	if s.localhost == "" || !local(request.URL.Hostname()) {
		return s.next.RoundTrip(request)
	}
	onward := request.Clone(request.Context())
	host := s.localhost
	if port := request.URL.Port(); port != "" {
		host += ":" + port
	}
	onward.URL.Host, onward.Host = host, host
	return s.next.RoundTrip(onward)
}

// local reports whether the host is the local host: by that name, or as a
// loopback address.
func local(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)
	return err == nil && address.IsLoopback()
}

// Refused reports whether the error of a request is that of a refused
// address.
func Refused(err error) bool {
	return errors.Is(err, ErrRefused)
}
