package sandbox

import (
	"cmp"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"

	ateapi "go.ate.dev/substrate/api/ateapi/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// Egress says where a sandbox may connect. Nothing else is reached: the
// egress gateway of Substrate refuses it.
type Egress struct {
	// Hosts are reached as they are. The gateway passes their TLS through
	// unread, and puts nothing into their requests.
	Hosts []Host
	// Secrets go into the requests of their hosts. The gateway reads the TLS
	// of these hosts, with a certificate of its own that the sandbox trusts.
	Secrets []Secret
}

// Host is where a sandbox connects: a host name, or a pattern with "*" as
// its leftmost label, and a port.
type Host struct {
	// TLS is set for HTTPS.
	TLS  bool
	Name string
	Port int
}

// Secret goes into a header of the requests to its host: the gateway puts
// Prefix and the secret that URI names in place of the header's value. A
// request without the header goes on as it is.
type Secret struct {
	Host
	Header, Prefix, URI string
}

// HostOf returns the host of an http or https URL, with the port of its
// scheme unless it has one. The name of a URL may be "*", or begin with "*."
// for any host of a domain. An address in place of a name is refused, since
// the gateway connects to names only, and so is the local host, which would
// be the gateway's own.
func HostOf(address string) (Host, error) {
	at, err := url.Parse(address)
	if err != nil {
		return Host{}, err
	}
	host := Host{TLS: at.Scheme == "https", Name: strings.ToLower(at.Hostname()), Port: 80}
	if host.TLS {
		host.Port = 443
	}
	switch {
	case at.Scheme != "http" && at.Scheme != "https":
		return Host{}, fmt.Errorf("%s is not an http or https URL", address)
	case host.Name == "":
		return Host{}, fmt.Errorf("%s has no host", address)
	}
	if _, err := netip.ParseAddr(host.Name); err == nil {
		return Host{}, fmt.Errorf("%s has an address in place of a host name, which sandboxes cannot reach", address)
	}
	if host.Name == "localhost" {
		return Host{}, fmt.Errorf("%s is at the local host, which sandboxes cannot reach", address)
	}
	if port := at.Port(); port != "" {
		if host.Port, err = strconv.Atoi(port); err != nil || host.Port < 1 || host.Port > 65535 {
			return Host{}, fmt.Errorf("%s has an invalid port", address)
		}
	}
	pattern := strings.TrimPrefix(host.Name, "*.")
	if host.Name != "*" && len(validation.IsDNS1123Subdomain(pattern)) > 0 {
		return Host{}, fmt.Errorf("%s does not have a valid host name", address)
	}
	return host, nil
}

// kind is the kind of rule of a host: HTTP, HTTPS that the gateway reads,
// and TLS that it passes through.
type kind int

const (
	httpRule kind = iota
	httpsRule
	passthroughRule
)

// key identifies a rule: one decides each name and port.
type key struct {
	name string
	port int
}

// rules returns the egress as the rules of a policy, in a stable order. A
// host whose requests get secrets is read, whatever else allows it.
func (e Egress) rules() ([]*ateapi.EgressRule, error) {
	kinds := map[key]kind{}
	secrets := map[key][]*ateapi.CredentialHeader{}
	for _, host := range e.Hosts {
		at := key{host.Name, host.Port}
		if _, taken := kinds[at]; !taken {
			kinds[at] = httpRule
			if host.TLS {
				kinds[at] = passthroughRule
			}
		}
	}
	for _, secret := range e.Secrets {
		at := key{secret.Name, secret.Port}
		kinds[at] = httpRule
		if secret.TLS {
			kinds[at] = httpsRule
		}
		header := strings.ToLower(secret.Header)
		for _, given := range secrets[at] {
			if given.Header == header {
				return nil, fmt.Errorf("two secrets for the header %s of %s", header, secret.Name)
			}
		}
		secrets[at] = append(secrets[at], &ateapi.CredentialHeader{Header: header, Prefix: secret.Prefix, CredentialUri: secret.URI})
	}
	keys := make([]key, 0, len(kinds))
	for at := range kinds {
		keys = append(keys, at)
	}
	slices.SortFunc(keys, func(a, b key) int { return cmp.Or(cmp.Compare(a.name, b.name), cmp.Compare(a.port, b.port)) })
	rules := make([]*ateapi.EgressRule, 0, len(keys))
	for _, at := range keys {
		ports := &ateapi.Ports{Numbers: []int32{int32(at.port)}}
		var effects *ateapi.HttpRuleEffects
		if given := secrets[at]; len(given) > 0 {
			slices.SortFunc(given, func(a, b *ateapi.CredentialHeader) int { return cmp.Compare(a.Header, b.Header) })
			effects = &ateapi.HttpRuleEffects{ReplaceHeaders: given}
		}
		hostnames := []string{at.name}
		switch kinds[at] {
		case httpRule:
			rules = append(rules, &ateapi.EgressRule{Http: &ateapi.HttpRule{Hostnames: hostnames, Ports: ports, Effects: effects}})
		case httpsRule:
			rules = append(rules, &ateapi.EgressRule{Https: &ateapi.HttpsRule{Hostnames: hostnames, Ports: ports, Effects: effects}})
		case passthroughRule:
			rules = append(rules, &ateapi.EgressRule{TlsPassthrough: &ateapi.TlsPassthroughRule{Hostnames: hostnames, Ports: ports}})
		}
	}
	return rules, nil
}
