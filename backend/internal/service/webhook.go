package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// The endpoint is operator configuration, not a rule/user-supplied URL. Internal
// webhooks are supported; redirects and cloud metadata/link-local destinations
// are not. Resolve and dial the checked IP, preventing DNS rebind between checks.
func newWebhookClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxConnsPerHost = 4
	transport.ResponseHeaderTimeout = timeout
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, ip := range ips {
			if ip.IP.IsLinkLocalUnicast() || ip.IP.IsLinkLocalMulticast() || ip.IP.IsUnspecified() || ip.IP.IsMulticast() {
				return nil, fmt.Errorf("webhook destination is not allowed")
			}
		}
		var last error
		for _, ip := range ips {
			conn, err := (&net.Dialer{Timeout: timeout}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last == nil {
			last = fmt.Errorf("webhook has no destination addresses")
		}
		return nil, last
	}
	return &http.Client{Timeout: timeout, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
