package common

import (
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/suite"
)

type NetworkSuite struct {
	suite.Suite
}

func TestNetworkSuite(t *testing.T) {
	suite.Run(t, new(NetworkSuite))
}

// mustCIDR parses a CIDR string and panics on error.
func mustCIDR(s string) *net.IPNet {
	ip, ipNet, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	ipNet.IP = ip // keep host bits, mirroring net.Interfaces output
	return ipNet
}

func staticLister(cidrs ...*net.IPNet) cidrLister {
	return func() ([]*net.IPNet, error) { return cidrs, nil }
}

func errLister(msg string) cidrLister {
	return func() ([]*net.IPNet, error) { return nil, errors.New(msg) }
}

// --- FindIpForPeers ---

// Reproduces canonical/microceph#476: pick eth0, not the docker bridge.
func (s *NetworkSuite) TestFindIpForPeers_PicksMatchingSubnet() {
	nw := &networkImpl{
		lister: staticLister(
			mustCIDR("172.17.0.1/16"),   // docker0
			mustCIDR("192.168.1.50/24"), // eth0
		),
	}

	ip, err := nw.FindIpForPeers([]string{"192.168.1.10:7443"})
	s.Require().NoError(err)
	s.Equal("192.168.1.50", ip)
}

func (s *NetworkSuite) TestFindIpForPeers_BarePeerAddress() {
	nw := &networkImpl{lister: staticLister(mustCIDR("10.0.0.5/24"))}

	ip, err := nw.FindIpForPeers([]string{"10.0.0.42"})
	s.Require().NoError(err)
	s.Equal("10.0.0.5", ip)
}

func (s *NetworkSuite) TestFindIpForPeers_IPv6() {
	nw := &networkImpl{lister: staticLister(mustCIDR("2001:db8::5/64"))}

	ip, err := nw.FindIpForPeers([]string{"[2001:db8::10]:7443"})
	s.Require().NoError(err)
	s.Equal("2001:db8::5", ip)
}

func (s *NetworkSuite) TestFindIpForPeers_MultiplePeersOneMatches() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	ip, err := nw.FindIpForPeers([]string{
		"10.99.0.1:7443",    // unreachable
		"192.168.1.10:7443", // reachable
	})
	s.Require().NoError(err)
	s.Equal("192.168.1.50", ip)
}

func (s *NetworkSuite) TestFindIpForPeers_NoMatch() {
	nw := &networkImpl{lister: staticLister(mustCIDR("172.17.0.1/16"))}

	_, err := nw.FindIpForPeers([]string{"192.168.1.10:7443"})
	s.Require().Error(err)
	s.Contains(err.Error(), "no local interface shares a subnet")
}

func (s *NetworkSuite) TestFindIpForPeers_SkipsNonGlobalUnicast() {
	nw := &networkImpl{
		lister: staticLister(
			mustCIDR("169.254.1.5/16"),  // link-local, skipped
			mustCIDR("192.168.1.50/24"), // matches
		),
	}

	ip, err := nw.FindIpForPeers([]string{"192.168.1.10:7443"})
	s.Require().NoError(err)
	s.Equal("192.168.1.50", ip)
}

func (s *NetworkSuite) TestFindIpForPeers_EmptyPeers() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	_, err := nw.FindIpForPeers(nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "no peer addresses supplied")
}

func (s *NetworkSuite) TestFindIpForPeers_InvalidPeer() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	_, err := nw.FindIpForPeers([]string{"not-an-address"})
	s.Require().Error(err)
	s.Contains(err.Error(), "invalid peer address")
}

func (s *NetworkSuite) TestFindIpForPeers_ListerError() {
	nw := &networkImpl{lister: errLister("boom")}

	_, err := nw.FindIpForPeers([]string{"192.168.1.10:7443"})
	s.Require().Error(err)
	s.Contains(err.Error(), "boom")
}

// --- FindIpOnSubnet ---

func (s *NetworkSuite) TestFindIpOnSubnet_Match() {
	nw := &networkImpl{
		lister: staticLister(
			mustCIDR("172.17.0.1/16"),
			mustCIDR("192.168.1.50/24"),
		),
	}

	ip, err := nw.FindIpOnSubnet("192.168.1.0/24")
	s.Require().NoError(err)
	s.Equal("192.168.1.50", ip)
}

func (s *NetworkSuite) TestFindIpOnSubnet_NoMatch() {
	nw := &networkImpl{lister: staticLister(mustCIDR("172.17.0.1/16"))}

	_, err := nw.FindIpOnSubnet("192.168.1.0/24")
	s.Require().Error(err)
}

func (s *NetworkSuite) TestFindIpOnSubnet_InvalidCIDR() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	_, err := nw.FindIpOnSubnet("garbage")
	s.Require().Error(err)
}

// --- FindNetworkAddress ---

func (s *NetworkSuite) TestFindNetworkAddress_Match() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	cidr, err := nw.FindNetworkAddress("192.168.1.50")
	s.Require().NoError(err)
	s.Equal("192.168.1.50/24", cidr)
}

func (s *NetworkSuite) TestFindNetworkAddress_NotPresent() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	_, err := nw.FindNetworkAddress("10.0.0.1")
	s.Require().Error(err)
}

func (s *NetworkSuite) TestFindNetworkAddress_InvalidIP() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	_, err := nw.FindNetworkAddress("garbage")
	s.Require().Error(err)
}

// --- IsIpOnSubnet ---

func (s *NetworkSuite) TestIsIpOnSubnet() {
	nw := &networkImpl{}

	s.True(nw.IsIpOnSubnet("192.168.1.10", "192.168.1.0/24"))
	s.False(nw.IsIpOnSubnet("10.0.0.1", "192.168.1.0/24"))
	s.False(nw.IsIpOnSubnet("garbage", "192.168.1.0/24"))
	s.False(nw.IsIpOnSubnet("192.168.1.10", "garbage"))
}
