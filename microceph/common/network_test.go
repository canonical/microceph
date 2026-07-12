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

// --- ParseSubnetList ---

func (s *NetworkSuite) TestParseSubnetList_Single() {
	nets, err := ParseSubnetList("10.0.0.0/24")
	s.Require().NoError(err)
	s.Require().Len(nets, 1)
	s.Equal("10.0.0.0/24", nets[0].String())
}

func (s *NetworkSuite) TestParseSubnetList_Multiple() {
	nets, err := ParseSubnetList("10.0.0.0/24,172.16.0.0/16")
	s.Require().NoError(err)
	s.Require().Len(nets, 2)
	s.Equal("10.0.0.0/24", nets[0].String())
	s.Equal("172.16.0.0/16", nets[1].String())
}

func (s *NetworkSuite) TestParseSubnetList_TrimsWhitespace() {
	nets, err := ParseSubnetList("  10.0.0.0/24 ,  172.16.0.0/16 ")
	s.Require().NoError(err)
	s.Require().Len(nets, 2)
	s.Equal("10.0.0.0/24", nets[0].String())
	s.Equal("172.16.0.0/16", nets[1].String())
}

func (s *NetworkSuite) TestParseSubnetList_MixedFamilies() {
	nets, err := ParseSubnetList("10.0.0.0/24,2001:db8::/64")
	s.Require().NoError(err)
	s.Require().Len(nets, 2)
	s.Equal("10.0.0.0/24", nets[0].String())
	s.Equal("2001:db8::/64", nets[1].String())
}

func (s *NetworkSuite) TestParseSubnetList_PreservesOrder() {
	nets, err := ParseSubnetList("172.16.0.0/16,10.0.0.0/24,192.168.1.0/24")
	s.Require().NoError(err)
	s.Require().Len(nets, 3)
	s.Equal("172.16.0.0/16", nets[0].String())
	s.Equal("10.0.0.0/24", nets[1].String())
	s.Equal("192.168.1.0/24", nets[2].String())
}

func (s *NetworkSuite) TestParseSubnetList_NoDeduplication() {
	nets, err := ParseSubnetList("10.0.0.0/24,10.0.0.0/24")
	s.Require().NoError(err)
	s.Require().Len(nets, 2)
}

func (s *NetworkSuite) TestParseSubnetList_EmptyInput() {
	_, err := ParseSubnetList("")
	s.Require().Error(err)
	s.Contains(err.Error(), "empty subnet list")
}

func (s *NetworkSuite) TestParseSubnetList_WhitespaceOnly() {
	_, err := ParseSubnetList("   ")
	s.Require().Error(err)
	s.Contains(err.Error(), "empty subnet list")
}

func (s *NetworkSuite) TestParseSubnetList_TrailingComma() {
	_, err := ParseSubnetList("10.0.0.0/24,")
	s.Require().Error(err)
	s.Contains(err.Error(), "empty entry")
}

func (s *NetworkSuite) TestParseSubnetList_DoubleComma() {
	_, err := ParseSubnetList("10.0.0.0/24,,172.16.0.0/16")
	s.Require().Error(err)
	s.Contains(err.Error(), "empty entry")
}

func (s *NetworkSuite) TestParseSubnetList_InvalidCIDR() {
	_, err := ParseSubnetList("10.0.0.0/24,garbage")
	s.Require().Error(err)
	s.Contains(err.Error(), `"garbage"`)
}

// --- FindIpOnSubnet ---

func (s *NetworkSuite) TestFindIpOnSubnet_SingleEntryParity() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	ip, err := nw.FindIpOnSubnet("192.168.1.0/24")
	s.Require().NoError(err)
	s.Equal("192.168.1.50", ip)
}

// First listed subnet that has a local IP wins.
func (s *NetworkSuite) TestFindIpOnSubnet_FirstListedMatchWins() {
	nw := &networkImpl{
		lister: staticLister(
			mustCIDR("10.0.0.5/24"),     // matches subnet #2
			mustCIDR("192.168.1.50/24"), // matches subnet #3
		),
	}

	ip, err := nw.FindIpOnSubnet("172.16.0.0/16,10.0.0.0/24,192.168.1.0/24")
	s.Require().NoError(err)
	s.Equal("10.0.0.5", ip)
}

func (s *NetworkSuite) TestFindIpOnSubnet_MixedFamiliesIPv6Match() {
	nw := &networkImpl{
		lister: staticLister(
			mustCIDR("2001:db8::5/64"),
		),
	}

	ip, err := nw.FindIpOnSubnet("10.0.0.0/24,2001:db8::/64")
	s.Require().NoError(err)
	s.Equal("2001:db8::5", ip)
}

func (s *NetworkSuite) TestFindIpOnSubnet_NoMatch() {
	nw := &networkImpl{lister: staticLister(mustCIDR("172.17.0.1/16"))}

	_, err := nw.FindIpOnSubnet("10.0.0.0/24,192.168.1.0/24")
	s.Require().Error(err)
	s.Contains(err.Error(), "no local IP")
	s.Contains(err.Error(), "10.0.0.0/24")
	s.Contains(err.Error(), "192.168.1.0/24")
}

func (s *NetworkSuite) TestFindIpOnSubnet_InvalidList() {
	nw := &networkImpl{lister: staticLister(mustCIDR("192.168.1.50/24"))}

	_, err := nw.FindIpOnSubnet("garbage")
	s.Require().Error(err)
}

// --- IsIpOnSubnet ---

func (s *NetworkSuite) TestIsIpOnSubnet_SingleEntryMatch() {
	nw := &networkImpl{}
	s.True(nw.IsIpOnSubnet("192.168.1.10", "192.168.1.0/24"))
}

func (s *NetworkSuite) TestIsIpOnSubnet_SecondEntryMatch() {
	nw := &networkImpl{}
	s.True(nw.IsIpOnSubnet("10.0.0.5", "192.168.1.0/24,10.0.0.0/24"))
}

func (s *NetworkSuite) TestIsIpOnSubnet_NoMatch() {
	nw := &networkImpl{}
	s.False(nw.IsIpOnSubnet("172.16.0.1", "192.168.1.0/24,10.0.0.0/24"))
}

func (s *NetworkSuite) TestIsIpOnSubnet_InvalidList() {
	nw := &networkImpl{}
	s.False(nw.IsIpOnSubnet("192.168.1.10", "garbage"))
}

func (s *NetworkSuite) TestIsIpOnSubnet_InvalidAddress() {
	nw := &networkImpl{}
	s.False(nw.IsIpOnSubnet("not-an-ip", "192.168.1.0/24"))
}

func (s *NetworkSuite) TestIsIpOnSubnet_IPv6() {
	nw := &networkImpl{}
	s.True(nw.IsIpOnSubnet("2001:db8::10", "10.0.0.0/24,2001:db8::/64"))
}
