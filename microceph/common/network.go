package common

import (
	"fmt"
	"net"

	"github.com/canonical/microceph/microceph/logger"
)

type NetworkIntf interface {
	FindIpOnSubnet(subnet string) (string, error)
	FindNetworkAddress(address string) (string, error)
	IsIpOnSubnet(address string, subnet string) bool
}

type networkImpl struct{}

// FindIpOnSubnet scans the host's network interfaces to check if an IP is available
// for the provided subnet. It returns the FIRST found IP address or an empty string
// in case of errors.
func (nwi networkImpl) FindIpOnSubnet(subnet string) (string, error) {
	_, sn, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", err
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			logger.Warnf("error fetching network interfaces: %v", err)
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			if !ipNet.IP.IsGlobalUnicast() {
				continue
			}

			if sn.Contains(ipNet.IP) {
				return ipNet.IP.String(), nil
			}
		}
	}
	return "", fmt.Errorf("no IP belongs to provided subnet %s", subnet)
}

// FindNetworkAddress locates the provided IP address on host's network interfaces.
// It returns the containing subnet address on success or an empty string on failure.
func (nwi networkImpl) FindNetworkAddress(address string) (string, error) {
	nw := []string{}

	// Parse provided address.
	monIp := net.ParseIP(address)
	if monIp == nil {
		return "", fmt.Errorf("provided address %s is invalid", address)
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return "", err
	}

	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			logger.Warnf("error fetching network interfaces: %v", err)
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			if !ipNet.IP.IsGlobalUnicast() {
				continue
			}

			// record for reporting
			nw = append(nw, addr.String())
			if ipNet.IP.Equal(monIp) {
				return addr.String(), nil
			}
		}
	}

	return "", fmt.Errorf("provided mon-ip (%s) does not belong to any suitable network: %v", monIp, nw)
}

// IsIpOnSubnet checks if the provided ip address is on the provided subnet.
func (nwi networkImpl) IsIpOnSubnet(address string, subnet string) bool {
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

var Network NetworkIntf = networkImpl{}
