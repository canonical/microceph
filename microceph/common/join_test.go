package common

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/suite"
)

type JoinSuite struct {
	suite.Suite
}

func TestJoinSuite(t *testing.T) {
	suite.Run(t, new(JoinSuite))
}

// makeToken builds a base64-encoded join-token blob with the given addresses.
func makeToken(joinAddrsJSON string) string {
	body := `{"secret":"s","fingerprint":"f","join_addresses":` + joinAddrsJSON + `}`
	return base64.StdEncoding.EncodeToString([]byte(body))
}

func (s *JoinSuite) TestJoinTokenPeers_IPv4() {
	tok := makeToken(`["192.168.1.10:7443","192.168.1.11:7443"]`)

	peers, err := JoinTokenPeers(tok)
	s.Require().NoError(err)
	s.Equal([]string{"192.168.1.10:7443", "192.168.1.11:7443"}, peers)
}

func (s *JoinSuite) TestJoinTokenPeers_IPv6() {
	tok := makeToken(`["[2001:db8::10]:7443"]`)

	peers, err := JoinTokenPeers(tok)
	s.Require().NoError(err)
	s.Equal([]string{"[2001:db8::10]:7443"}, peers)
}

func (s *JoinSuite) TestJoinTokenPeers_InvalidBase64() {
	_, err := JoinTokenPeers("!!!not-base64!!!")
	s.Require().Error(err)
	s.Contains(err.Error(), "decode token")
}

func (s *JoinSuite) TestJoinTokenPeers_InvalidJSON() {
	tok := base64.StdEncoding.EncodeToString([]byte("{not json"))

	_, err := JoinTokenPeers(tok)
	s.Require().Error(err)
	s.Contains(err.Error(), "parse token")
}

func (s *JoinSuite) TestJoinTokenPeers_EmptyAddresses() {
	tok := makeToken(`[]`)

	_, err := JoinTokenPeers(tok)
	s.Require().Error(err)
	s.Contains(err.Error(), "no join addresses")
}

func (s *JoinSuite) TestJoinTokenPeers_MissingField() {
	tok := base64.StdEncoding.EncodeToString([]byte(`{"secret":"s"}`))

	_, err := JoinTokenPeers(tok)
	s.Require().Error(err)
	s.Contains(err.Error(), "no join addresses")
}
