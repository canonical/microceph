package mocks

import (
	"crypto/x509"
	"net/url"

	mcTypes "github.com/canonical/microcluster/v3/microcluster/types"
	"github.com/stretchr/testify/mock"
)

// ConnectorInterface is a mock of the microcluster Connector interface.
type ConnectorInterface struct {
	mock.Mock
}

// Cluster mocks the Cluster method.
func (m *ConnectorInterface) Cluster(isNotification bool) (mcTypes.Clients, error) {
	args := m.Called(isNotification)
	return args.Get(0).(mcTypes.Clients), args.Error(1)
}

// Leader mocks the Leader method.
func (m *ConnectorInterface) Leader(isNotification bool) (mcTypes.Client, error) {
	args := m.Called(isNotification)
	return args.Get(0).(mcTypes.Client), args.Error(1)
}

// Member mocks the Member method.
func (m *ConnectorInterface) Member(url *url.URL, isNotification bool, cert *x509.Certificate) (mcTypes.Client, error) {
	args := m.Called(url, isNotification, cert)
	return args.Get(0).(mcTypes.Client), args.Error(1)
}

// RandomMember mocks the RandomMember method.
func (m *ConnectorInterface) RandomMember(isNotification bool) (mcTypes.Client, error) {
	args := m.Called(isNotification)
	return args.Get(0).(mcTypes.Client), args.Error(1)
}

// NewConnectorInterface creates a new instance of ConnectorInterface. It also registers a testing interface on the mock and a cleanup function to assert the mocks expectations.
func NewConnectorInterface(t interface {
	mock.TestingT
	Cleanup(func())
}) *ConnectorInterface {
	mock := &ConnectorInterface{}
	mock.Mock.Test(t)

	t.Cleanup(func() { mock.AssertExpectations(t) })

	return mock
}
