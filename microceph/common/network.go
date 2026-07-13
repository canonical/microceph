package common

import (
	"fmt"
	"net"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

// NetworkIntf defines the network helper operations available to MicroCeph
// components. Implementations are injectable for testing via the cidrLister
// mechanism.
type NetworkIntf interface {
	FindIpOnSubnet(subnets string) (string, error)
	FindNetworkAddress(address string) (string, error)
	IsIpOnSubnet(address string, subnets string) bool
	FindIpForPeers(peers []string) (string, error)
}

// cidrLister returns the host's interface addresses. Injectable for tests.
type cidrLister func() ([]*net.IPNet, error)

// hostCIDRs is the production cidrLister, backed by net.Interfaces.
func hostCIDRs() ([]*net.IPNet, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	var cidrs []*net.IPNet
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			logger.Warnf("error fetching addresses for interface %s: %v", iface.Name, err)
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			cidrs = append(cidrs, ipNet)
		}
	}
	return cidrs, nil
}

type networkImpl struct {
	lister cidrLister
}

// FindIpOnSubnet returns the first local interface address that lies within
// any subnet in the comma-delimited CIDR list. A single CIDR is accepted as a
// one-element list. Subnets are iterated in input order, so the first listed
// subnet that has a local-IP match wins.
func (nwi *networkImpl) FindIpOnSubnet(subnets string) (string, error) {
	parsed, err := ParseSubnetList(subnets)
	if err != nil {
		return "", err
	}

	cidrs, err := nwi.lister()
	if err != nil {
		return "", err
	}

	for _, sn := range parsed {
		for _, ipNet := range cidrs {
			if !ipNet.IP.IsGlobalUnicast() {
				continue
			}
			if sn.Contains(ipNet.IP) {
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no local IP belongs to any of the listed subnets [%s]", subnets)
}

// FindNetworkAddress returns the local CIDR that owns the given IP.
func (nwi *networkImpl) FindNetworkAddress(address string) (string, error) {
	nw := []string{}
	address = strings.TrimSpace(address)

	monIP := net.ParseIP(address)
	if monIP == nil {
		return "", fmt.Errorf("provided address %s is invalid", address)
	}

	cidrs, err := nwi.lister()
	if err != nil {
		return "", err
	}

	for _, ipNet := range cidrs {
		if !ipNet.IP.IsGlobalUnicast() {
			continue
		}
		nw = append(nw, ipNet.String())
		if ipNet.IP.Equal(monIP) {
			return ipNet.String(), nil
		}
	}

	return "", fmt.Errorf("provided mon-ip (%s) does not belong to any suitable network: %v", monIP, nw)
}

// IsIpOnSubnet reports whether address lies within any subnet in the
// comma-delimited CIDR list. A single CIDR is accepted as a one-element list.
// Returns false (not an error) on parse failure.
func (nwi *networkImpl) IsIpOnSubnet(address string, subnets string) bool {
	address = strings.TrimSpace(address)
	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}

	parsed, err := ParseSubnetList(subnets)
	if err != nil {
		return false
	}

	for _, sn := range parsed {
		if sn.Contains(ip) {
			return true
		}
	}
	return false
}

// FindIpForPeers returns the first local interface address whose subnet
// contains any of the supplied peers. Each peer may be a bare IP or an
// "ip:port" / "[ipv6]:port" pair (the form carried in a join token).
func (nwi *networkImpl) FindIpForPeers(peers []string) (string, error) {
	if len(peers) == 0 {
		return "", fmt.Errorf("no peer addresses supplied")
	}

	peerIPs := make([]net.IP, 0, len(peers))
	for _, peer := range peers {
		ip, err := parsePeerIP(peer)
		if err != nil {
			return "", fmt.Errorf("invalid peer address %q: %w", peer, err)
		}
		peerIPs = append(peerIPs, ip)
	}

	cidrs, err := nwi.lister()
	if err != nil {
		return "", err
	}

	for _, ipNet := range cidrs {
		if !ipNet.IP.IsGlobalUnicast() {
			continue
		}
		for _, peerIP := range peerIPs {
			if ipNet.Contains(peerIP) {
				return ipNet.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no local interface shares a subnet with any of the cluster peers %v", peers)
}

// ParseSubnetList parses a comma-delimited CIDR list. Whitespace around
// entries is trimmed. Empty input and empty entries (e.g. trailing comma,
// "a,,b") are rejected. Order is preserved. No deduplication.
func ParseSubnetList(s string) ([]*net.IPNet, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty subnet list")
	}

	parts := strings.Split(s, ",")
	nets := make([]*net.IPNet, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty entry in subnet list %q", s)
		}
		_, n, err := net.ParseCIDR(part)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q in subnet list: %w", part, err)
		}
		nets = append(nets, n)
	}
	return nets, nil
}

// parsePeerIP extracts the IP from a bare-IP or "ip:port" string.
func parsePeerIP(peer string) (net.IP, error) {
	peer = strings.TrimSpace(peer)
	if ip := net.ParseIP(peer); ip != nil {
		return ip, nil
	}
	host, _, err := net.SplitHostPort(peer)
	if err != nil {
		return nil, err
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("not an IP address")
	}
	return ip, nil
}

// Network is the package-level NetworkIntf used by MicroCeph components;
// tests override it with a mock.
var Network NetworkIntf = &networkImpl{lister: hostCIDRs}
