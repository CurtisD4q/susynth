// Package values gives names to the things programs take and produce: an
// address, a name, a web address, a host and port, a network range, a path.
// Each is checked once and kept typed, so a result from one tool can be
// offered to the next in the form that tool needs, and nothing that could
// be read as an option (a leading "-") reaches a command line.
package values

import (
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Kind says what a Value is.
type Kind string

// The kinds. Host is not a kind a Value has; it is what a tool asks for when
// either an IP address or a domain name will do.
const (
	Host     Kind = "host"
	IP       Kind = "ip"
	Domain   Kind = "domain"
	URL      Kind = "url"
	Endpoint Kind = "endpoint" // a host and a port, such as 10.0.0.5:443
	Range    Kind = "range"    // a network in CIDR form, such as 10.0.0.0/24
)

// Value is a piece of text that has been checked and given a kind.
type Value struct {
	Kind Kind
	Text string
}

var label = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

// Parse checks s and says what it is. It refuses anything a tool could take
// for an option (a leading dash), anything with spaces or control
// characters, and anything it cannot name.
func Parse(s string) (Value, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 2048 || strings.HasPrefix(s, "-") {
		return Value{}, false
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r > 0x7e {
			return Value{}, false
		}
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || !validHost(u.Hostname()) {
			return Value{}, false
		}
		return Value{URL, s}, true
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return Value{Range, p.Masked().String()}, true
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return Value{IP, a.String()}, true
	}
	if host, port, ok := splitHostPort(s); ok {
		return Value{Endpoint, hostPort(host, port)}, true
	}
	if d, ok := domain(s); ok {
		return Value{Domain, d}, true
	}
	return Value{}, false
}

func validHost(h string) bool {
	if _, err := netip.ParseAddr(h); err == nil {
		return true
	}
	_, ok := domain(h)
	return ok
}

func domain(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSuffix(s, "."))
	if s == "" || len(s) > 253 {
		return "", false
	}
	labels := strings.Split(s, ".")
	for _, l := range labels {
		if !label.MatchString(l) {
			return "", false
		}
	}
	// A name never ends in a number, so 10.0.0.256 is a mistyped address,
	// not a domain.
	if last := labels[len(labels)-1]; strings.Trim(last, "0123456789") == "" {
		return "", false
	}
	return s, true
}

func splitHostPort(s string) (string, int, bool) {
	host, p, err := net.SplitHostPort(s)
	if err != nil {
		return "", 0, false
	}
	port, err := strconv.Atoi(p)
	if err != nil || port < 1 || port > 65535 || !validHost(host) {
		return "", 0, false
	}
	if a, err := netip.ParseAddr(host); err == nil {
		host = a.String()
	} else {
		host = strings.ToLower(strings.TrimSuffix(host, "."))
	}
	return host, port, true
}

func hostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// MakeEndpoint makes the Value for a host and port. ok is false if either is
// not valid.
func MakeEndpoint(host string, port int) (Value, bool) {
	h, p, ok := splitHostPort(hostPort(host, port))
	if !ok {
		return Value{}, false
	}
	return Value{Endpoint, hostPort(h, p)}, true
}

// HostText is the host part of an IP, domain, endpoint or web address.
func (v Value) HostText() string {
	switch v.Kind {
	case IP, Domain:
		return v.Text
	case Endpoint:
		h, _, _ := net.SplitHostPort(v.Text)
		return h
	case URL:
		u, err := url.Parse(v.Text)
		if err != nil {
			return ""
		}
		return strings.ToLower(u.Hostname())
	}
	return ""
}

// Port is the port of an endpoint, or the port a web address implies. It is 0
// for anything else.
func (v Value) Port() int {
	switch v.Kind {
	case Endpoint:
		_, p, _ := net.SplitHostPort(v.Text)
		n, _ := strconv.Atoi(p)
		return n
	case URL:
		u, err := url.Parse(v.Text)
		if err != nil {
			return 0
		}
		if p := u.Port(); p != "" {
			n, _ := strconv.Atoi(p)
			return n
		}
		if u.Scheme == "https" {
			return 443
		}
		return 80
	}
	return 0
}

// As turns v into something of kind k if it can: the host of a web address,
// a web address for a host. It does not look anything up.
func (v Value) As(k Kind) (Value, bool) {
	if v.Kind == k {
		return v, true
	}
	switch k {
	case Host:
		if h, ok := Parse(v.HostText()); ok && (h.Kind == IP || h.Kind == Domain) {
			return h, true
		}
	case IP, Domain:
		if h, ok := Parse(v.HostText()); ok && h.Kind == k {
			return h, true
		}
	case URL:
		switch v.Kind {
		case IP, Domain:
			return Value{URL, "https://" + hostForURL(v.Text)}, true
		case Endpoint:
			scheme := "http"
			if p := v.Port(); p == 443 || p == 8443 {
				scheme = "https"
			}
			return Value{URL, scheme + "://" + v.Text}, true
		}
	}
	return Value{}, false
}

func hostForURL(h string) string {
	if a, err := netip.ParseAddr(h); err == nil && a.Is6() {
		return "[" + h + "]"
	}
	return h
}
