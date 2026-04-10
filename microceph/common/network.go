package common

import (
	"fmt"
	"net"
	"strings"

	"github.com/canonical/microceph/microceph/logger"
)

type NetworkIntf interface {
	FindIpOnSubnet(subnet string) (string, error)
	FindNetworkAddress(address string) (string, error)
	IsIpOnSubnet(address string, subnet string) bool
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
// the given CIDR.
func (nwi *networkImpl) FindIpOnSubnet(subnet string) (string, error) {
	subnet = strings.TrimSpace(subnet)
	_, sn, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", err
	}

	cidrs, err := nwi.lister()
	if err != nil {
		return "", err
	}

	for _, ipNet := range cidrs {
		if !ipNet.IP.IsGlobalUnicast() {
			continue
		}
		if sn.Contains(ipNet.IP) {
			return ipNet.IP.String(), nil
		}
	}
	return "", fmt.Errorf("no IP belongs to provided subnet %s", subnet)
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

// IsIpOnSubnet checks if the provided ip address is on the provided subnet.
func (nwi *networkImpl) IsIpOnSubnet(address string, subnet string) bool {
	address = strings.TrimSpace(address)
	subnet = strings.TrimSpace(subnet)

	ip := net.ParseIP(address)
	if ip == nil {
		return false
	}

	_, sn, err := net.ParseCIDR(subnet)
	if err != nil {
		return false
	}

	return sn.Contains(ip)
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

var Network NetworkIntf = &networkImpl{lister: hostCIDRs}
