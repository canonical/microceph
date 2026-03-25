package main

import (
	"testing"

	"github.com/canonical/microceph/microceph/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type certificateSetRGWSuite struct {
	tests.BaseSuite
}

func TestCertificateSetRGW(t *testing.T) {
	suite.Run(t, new(certificateSetRGWSuite))
}

func (s *certificateSetRGWSuite) SetupTest() {
	s.BaseSuite.SetupTest()
}

func (s *certificateSetRGWSuite) TestValidateSSLInputsEmptyCert() {
	cmd := cmdCertificateSetRGW{
		flagSSLCertificate: "",
		flagSSLPrivateKey:  "c29tZWtleQ==",
	}
	err := cmd.validateSSLInputs()
	assert.ErrorContains(s.T(), err, "SSL certificate cannot be empty")
}

func (s *certificateSetRGWSuite) TestValidateSSLInputsEmptyKey() {
	cmd := cmdCertificateSetRGW{
		flagSSLCertificate: "c29tZWNlcnQ=",
		flagSSLPrivateKey:  "",
	}
	err := cmd.validateSSLInputs()
	assert.ErrorContains(s.T(), err, "SSL private key cannot be empty")
}

func (s *certificateSetRGWSuite) TestValidateSSLInputsInvalidBase64Cert() {
	cmd := cmdCertificateSetRGW{
		flagSSLCertificate: "not-valid-base64!",
		flagSSLPrivateKey:  "c29tZWtleQ==",
	}
	err := cmd.validateSSLInputs()
	assert.ErrorContains(s.T(), err, "failed to decode SSL certificate")
}

func (s *certificateSetRGWSuite) TestValidateSSLInputsInvalidBase64Key() {
	cmd := cmdCertificateSetRGW{
		flagSSLCertificate: "c29tZWNlcnQ=",
		flagSSLPrivateKey:  "not-valid-base64!",
	}
	err := cmd.validateSSLInputs()
	assert.ErrorContains(s.T(), err, "failed to decode SSL private key")
}

func (s *certificateSetRGWSuite) TestValidateSSLInputsValid() {
	cmd := cmdCertificateSetRGW{
		flagSSLCertificate: "c29tZWNlcnQ=",
		flagSSLPrivateKey:  "c29tZWtleQ==",
	}
	err := cmd.validateSSLInputs()
	assert.NoError(s.T(), err)
}
