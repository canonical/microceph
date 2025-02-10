package ceph

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/microceph/microceph/tests"

	"github.com/canonical/microceph/microceph/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type rgwSuite struct {
	tests.BaseSuite
	TestStateInterface *mocks.StateInterface
}

const validSSLCertificate = `LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURuakNDQW9hZ0F3SUJBZ0lVR0czNU9mWkcrRFdFQytrc2FHalJyTmlXZncwd0RRWUpLb1pJaHZjTkFRRUwKQlFBd1hERUxNQWtHQTFVRUJoTUNWVk14RHpBTkJnTlZCQWdNQmtSbGJtbGhiREVVTUJJR0ExVUVCd3dMVTNCeQphVzVuWm1sbGJHUXhEREFLQmdOVkJBb01BMFJwY3pFWU1CWUdBMVVFQXd3UGQzZDNMbVY0WVcxd2JHVXVZMjl0Ck1CNFhEVEkwTURneE5qRTROREUxT0ZvWERUSTFNRGd4TmpFNE5ERTFPRm93WERFTE1Ba0dBMVVFQmhNQ1ZWTXgKRHpBTkJnTlZCQWdNQmtSbGJtbGhiREVVTUJJR0ExVUVCd3dMVTNCeWFXNW5abWxsYkdReEREQUtCZ05WQkFvTQpBMFJwY3pFWU1CWUdBMVVFQXd3UGQzZDNMbVY0WVcxd2JHVXVZMjl0TUlJQklqQU5CZ2txaGtpRzl3MEJBUUVGCkFBT0NBUThBTUlJQkNnS0NBUUVBdFl5ZGRhb0l4T3hQWmtVMEN1dXE0aEd3Q2JlZXBUM3lBQ0JOS1J6MjB5alQKZ2xSWTFTSTlXSjl4K2t1a3dMTGNiVEIrSkNka2NWTEZuNThtVDRmUW5IMHdmWCtIby9BTUNHNkxITnZnOXovVAorTlV4dTgydGZsVko3RFRUdmVuYzlqVU9qNFZqUExaV2tiemNIOC91Sm1DNkd1ZzAvcksvN2wraG9xNUd6VXhzCmJQeGlOV0QvNW5kaklKa1VidEtpTllnQlRwcnRzZFlCWHoyeTFxS1AxcGZLQ3VIUWVldTNLTWErS0dUU2NUSjYKU251Y0pxZmIvTWdUMWozV3Zpcm1QaUQ3bEwzY3ZmaEtmTEgvYTdsaFhIeDRic21TekZ2UkRYTCt1YmNhak5seQpGUm5WdG9hUHhmMUY4RStFbXh4cXNESlc2bHZKVHJMeW84TjVNbWtoOFFJREFRQUJvMWd3VmpBVUJnTlZIUkVFCkRUQUxnZ2xzYjJOaGJHaHZjM1F3SFFZRFZSME9CQllFRkhIMFoxdWVmSHB1Wll1QTRzRFBlWTd4U2R6b01COEcKQTFVZEl3UVlNQmFBRlBRc1Q0SkU3dUl1ay96T2VvVlZpQVZYeDBoUk1BMEdDU3FHU0liM0RRRUJDd1VBQTRJQgpBUUNucHVFM2hzVHAwckZCU1hWRnV6VzExZjE2bXlML3pyZkJDWnRxQyt6UFZINGlyUUlrRFg2TDdPekY2K00vCml6OFJtQlZXSVpzWTlzczM5SmRlcEsvOVhuMEo5RUdDS2hhdmpldS8yUnpvalFaeXRQWU5DdldtMlhTQ0VHY2wKSDhDcGNQVC9JdnlCNU8yRVl0RUJNcnRrUVNKNjVFWlQyZHRiVFYySUdJN3ZDdjJIUnY0Y2twRXBFTWlLWnNPYgpBcWovbGNLeWFZODVwakFBWWVtVlprZ2dRZTJUM0tzSDFYRVJrNnhFRHF2TUdHbjEvOTNHY1J1enVTVTZaYXVPCmVzVDVISUl2UGZReWlwZG4rOWlKVjluc3hyNGVCa1JPVWFvV2s1NVVENE5tcEtiaHJ3MzZ3RzN4RzJ2RlIxeWUKSVFPNmhKMk5yckFnc2JwemxINzhVcjM4Ci0tLS0tRU5EIENFUlRJRklDQVRFLS0tLS0=`
const validSSLPrivateKey = `LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCk1JSUV2UUlCQURBTkJna3Foa2lHOXcwQkFRRUZBQVNDQktjd2dnU2pBZ0VBQW9JQkFRQzFqSjExcWdqRTdFOW0KUlRRSzY2cmlFYkFKdDU2bFBmSUFJRTBwSFBiVEtOT0NWRmpWSWoxWW4zSDZTNlRBc3R4dE1INGtKMlJ4VXNXZgpueVpQaDlDY2ZUQjlmNGVqOEF3SWJvc2MyK0QzUDlQNDFURzd6YTErVlVuc05OTzk2ZHoyTlE2UGhXTTh0bGFSCnZOd2Z6KzRtWUxvYTZEVCtzci91WDZHaXJrYk5UR3hzL0dJMVlQL21kMk1nbVJSdTBxSTFpQUZPbXUyeDFnRmYKUGJMV29vL1dsOG9LNGRCNTY3Y294cjRvWk5KeE1ucEtlNXdtcDl2OHlCUFdQZGErS3VZK0lQdVV2ZHk5K0VwOApzZjlydVdGY2ZIaHV5WkxNVzlFTmN2NjV0eHFNMlhJVkdkVzJoby9GL1VYd1Q0U2JIR3F3TWxicVc4bE9zdktqCncza3lhU0h4QWdNQkFBRUNnZ0VBQXVWdTM2RXFTYVh4Y0ZLN1RVOU1KeFljSmxPSkV0N0ZuUTNtM1RpS2tYek4KdnY4RWVjWDFqNVBmbUJ3YjBUMHBPZzZ6ZkhVcWE0cGovN05rdzVFSm1XMS8yQWl3UzhPNUZXdGFDY2hTTXUrUQpQS0IrRGg1dVhaMFR0RkoxYkVxdVRUazBkY0t0ZmhyMGo1ZWhOVnEyVkdObnBLVStyeTkvMDFnd05tMnNVSHNZClBmZWszNjNXRU5BOXlqbDNuOXFicXp4aXphaVowekJEM1ZDWkhXRVBrd215Yk1oRnZQY1V4M24rU2tRUnRhYk0KSzdyZTc2bkwwdU9GdTV3L1FUUU5KVVcvdGJ5R0lZVFJHUExibTFJeWs4RUpmc2lWa09yR0tVWE1HelU1UlZ5QQpROGQvWlI1b3Q0L0R3R29OM2NmQytBODlXT1g2dk5kU3B6Y1VsZmtLS1FLQmdRRGtPbDlIRnRiRklZd0VZbmpDCklQMDdmcVArNnhtVnZpQlRjOEFqQnMraGc1NmhadFltQ29aOTQ4RHJZT0MxSEtWVi9WZWVCU2FHZGo4WUNtdnkKZUNkTExvYms3Skc0bHRiUmxMUlpHUGEyR1JSTkZUZ2xhRXQ2Q1F3QXFuQ1hUMkxoVHI1NGswVlZFT00wclFNWgpUN1NMSVdzZXA5N3N2TW15ZGwyWlNBbDNuUUtCZ1FETHBDSTV4UjF6eU9Kb1RQQStaRTIrYVlCMjdqM0VPeSs2CmdyTXovc3M2c2lndVJ4TEMvRHZJWUNLbXc0S213N2N0dUw3eW5UbDRuaFJIVFhwRDV5M0NLTG5OVTBXRVA5L1MKVmsrS25FUWFFdVdaTm9pbU1xUTNMSkcvL0pYYUpPa2c3WmV0eWt2cnYyUHdLY0lncHpKUGdJWjZOeVRzaDV5NwowbUFyVWF4bFpRS0JnUURON2hXV1VYZE12RzVZYm5uRHdIeCtPRkRGYldEU2lwRWtlNmI4YytMWk82Zmd2cWV2Ci80TkhDRUJFb2s5ZlhBK2JQVkxYbEpJa2RZR01zYXFoUitVOG95aTRXdlZKZDJFeURsbUVvMC9KRTJ3TCtYK0YKMFV0NU83eUd4VU4rWS9VMmt4U3VPMFF0ODJUdlhNVVZDNlErZmRMb0FGVFhpNmo2ekc2OEpoSFV5UUtCZ0VDMApyb3RjcnJjVHBaMHVsVWU5NTFZUmY5aEtheVhuQ0l0aTdENGhQOEl1eWNXcW43T0ZJaG5STWpGNi9oQ3ZMNDAvCm5xekllSEp6Q0U1L3Q5SExxeVorZWt0Ym9rTWJhS3NVOGNGQlZnSlM3dEY0R29OMG8rbEVLQ3V3dm96S0hhbHcKMVRsTGhrUXFWRDhEaGNPS1hOb1dKS1RBME9LM1ZIMzVvc1VnOW41aEFvR0FYYm45dHNOVFp1SmpLWXFxMWszVQovM2trR0NadEJnZmEvaCtpRWdPN1RoZFp5ekdzcjRuVGkzQTFyU09iVkZ0amoza3BOTEZCMW91aTVMcEJjMWFWCkQ0VjhuMHhDdktJbTl2N2hCVm9iTWZVZmVoVE1TSFBZOFZvcWJneXY4ZWZueS9MNVh6d2R3b0NXSGpEZFZXS3EKMVlDLzBIRkhlRFJzWm9aT3RtdTVnTTQ9Ci0tLS0tRU5EIFBSSVZBVEUgS0VZLS0tLS0=`

func TestRGW(t *testing.T) {
	suite.Run(t, new(rgwSuite))
}

// Expect: run ceph auth
func addRGWEnableExpectations(r *mocks.Runner) {
	// add keyring expectation
	r.On("RunCommand", tests.CmdAny("ceph", 9)...).Return("ok", nil).Once()
	// start service expectation
	r.On("RunCommand", []interface{}{
		"snapctl", "start", "microceph.rgw", "--enable",
	}...).Return("ok", nil).Once()
}

// Expect: run snapctl service stop
func addStopRGWExpectations(s *rgwSuite, r *mocks.Runner) {
	u := api.NewURL()

	state := &mocks.MockState{
		URL:         u,
		ClusterName: "foohost",
	}

	s.TestStateInterface.On("ClusterState").Return(state)
	r.On("RunCommand", tests.CmdAny("snapctl", 3)...).Return("ok", nil).Once()
}

// Set up test suite
func (s *rgwSuite) SetupTest() {
	s.BaseSuite.SetupTest()
	s.CopyCephConfigs()

	s.TestStateInterface = mocks.NewStateInterface(s.T())
}

// Test enabling RGW
func (s *rgwSuite) TestEnableRGW() {
	r := mocks.NewRunner(s.T())

	addRGWEnableExpectations(r)

	processExec = r

	err := EnableRGW(s.TestStateInterface, 8081, 443, "", "", []string{"10.1.1.1", "10.2.2.2"})

	assert.NoError(s.T(), err)

	// check that the radosgw.conf file contains expected values
	conf := s.ReadCephConfig("radosgw.conf")
	assert.Contains(s.T(), conf, "rgw frontends = beast port=8081\n")
	assert.Contains(s.T(), conf, "mon host = 10.1.1.1,10.2.2.2")
}

// Test enabling RGW
func (s *rgwSuite) TestEnableRGWWithInvalidSSLCertificate() {
	r := mocks.NewRunner(s.T())

	processExec = r

	err := EnableRGW(s.TestStateInterface, 80, 443, "invalid-certificate", validSSLPrivateKey, []string{"10.1.1.1", "10.2.2.2"})

	// we expect an illegal base64 data error
	assert.EqualError(s.T(), err, "illegal base64 data at input byte 7")

	// check that the radosgw.conf file contains expected values
	conf := s.ReadCephConfig("radosgw.conf")
	assert.Equal(s.T(), conf, "")
}

// Test enabling RGW
func (s *rgwSuite) TestEnableRGWWithInvalidSSLPrivateKey() {
	r := mocks.NewRunner(s.T())

	processExec = r

	err := EnableRGW(s.TestStateInterface, 80, 443, validSSLCertificate, "invalid-private-key", []string{"10.1.1.1", "10.2.2.2"})

	// we expect an illegal base64 data error
	assert.EqualError(s.T(), err, "illegal base64 data at input byte 7")

	// check that the radosgw.conf file contains expected values
	conf := s.ReadCephConfig("radosgw.conf")
	assert.Equal(s.T(), conf, "")
}

// Test enabling RGW
func (s *rgwSuite) TestEnableRGWWithMissingSSLCertificate() {
	r := mocks.NewRunner(s.T())

	addRGWEnableExpectations(r)

	processExec = r

	err := EnableRGW(s.TestStateInterface, 0, 443, "", validSSLPrivateKey, []string{"10.1.1.1", "10.2.2.2"})

	assert.NoError(s.T(), err)

	// check that the radosgw.conf file contains expected values
	conf := s.ReadCephConfig("radosgw.conf")
	assert.Contains(s.T(), conf, "rgw frontends = beast port=80\n")
}

// Test enabling RGW
func (s *rgwSuite) TestEnableRGWWithMissingSSLPrivateKey() {
	r := mocks.NewRunner(s.T())

	addRGWEnableExpectations(r)

	processExec = r

	err := EnableRGW(s.TestStateInterface, 0, 443, validSSLCertificate, "", []string{"10.1.1.1", "10.2.2.2"})

	assert.NoError(s.T(), err)

	// check that the radosgw.conf file contains expected values
	conf := s.ReadCephConfig("radosgw.conf")
	assert.Contains(s.T(), conf, "rgw frontends = beast port=80\n")
}

// Test enabling RGW
func (s *rgwSuite) TestEnableRGWWithSSL() {
	r := mocks.NewRunner(s.T())

	addRGWEnableExpectations(r)

	processExec = r

	err := EnableRGW(s.TestStateInterface, 8081, 443, validSSLCertificate, validSSLPrivateKey, []string{"10.1.1.1", "10.2.2.2"})

	assert.NoError(s.T(), err)

	// check that the radosgw.conf file contains expected values
	conf := s.ReadCephConfig("radosgw.conf")
	sslCertificatePath := filepath.Join(s.Tmp, "SNAP_COMMON", "server.crt")
	sslPrivateKeyPath := filepath.Join(s.Tmp, "SNAP_COMMON", "server.key")
	assert.Contains(s.T(), conf, "rgw frontends = beast port=8081 ssl_port=443 ssl_certificate="+sslCertificatePath+" ssl_private_key="+sslPrivateKeyPath+"\n")
}

func (s *rgwSuite) TestDisableRGW() {
	r := mocks.NewRunner(s.T())

	addStopRGWExpectations(s, r)

	processExec = r

	err := DisableRGW(context.Background(), s.TestStateInterface)

	// we expect a missing database error
	assert.EqualError(s.T(), err, "no server certificate")

	// check that the radosgw.conf file is absent
	_, err = os.Stat(filepath.Join(s.Tmp, "SNAP_DATA", "conf", "radosgw.conf"))
	assert.True(s.T(), os.IsNotExist(err))

	// check that the keyring file is absent
	_, err = os.Stat(filepath.Join(s.Tmp, "SNAP_COMMON", "data", "radosgw", "ceph-radosgw.gateway", "keyring"))
	assert.True(s.T(), os.IsNotExist(err))
}
