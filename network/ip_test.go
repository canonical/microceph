package network

import (
	"errors"
	"net"
	"testing"
	"time"
)

// Fake implementation of net.Conn
type conn struct {
	ip net.IP
}

func (c *conn) Read(b []byte) (n int, err error) {
	return 0, nil
}
func (c *conn) Write(b []byte) (n int, err error) {
	return 0, nil
}
func (c *conn) Close() error {
	return nil
}
func (c *conn) LocalAddr() net.Addr {
	var u net.UDPAddr
	u.IP = c.ip
	return &u
}
func (c *conn) RemoteAddr() net.Addr {
	return nil
}
func (c *conn) SetDeadline(t time.Time) error {
	return nil
}
func (c *conn) SetReadDeadline(t time.Time) error {
	return nil
}
func (c *conn) SetWriteDeadline(t time.Time) error {
	return nil
}

// Fake configurable Dialer fixture
type dialFixture struct {
	ip []*net.IP
}

func (d *dialFixture) fakeDialer(a string, b string) (net.Conn, error) {
	// Consume first element of dialFixture.ip Slice and either return fake
	// Conn object or error dependnig on contents. This is useful to test
	// code that makes multiple calls to the dialer expecting different
	// results.
	for n := len(d.ip); n > 0; {
		p := d.ip[0]
		d.ip = d.ip[1:]
		if p != nil {
			ip := *p
			myconn := conn{ip}
			var c net.Conn = &myconn
			return c, nil
		}
		return nil, errors.New("fake error")
	}
	return nil, errors.New("fake error")
}

// Tests
func TestInternalgetHostDefaultAddr(t *testing.T) {
	var df = dialFixture{[]*net.IP{nil}}
	var d = hostNetDirector{
		[]string{"[2001:db8::]:0", "192.0.2.0:0"},
		df.fakeDialer,
	}
	// Assert error return when Dialer errors for both v6 and v4 address
	result, err := d.getHostDefaultAddr()
	if result != nil && err != errors.New("fake error") {
		t.Errorf("getHostDefaultAddr = %s, %s; want nil", result, err)
	}
	// Assert we get v6 address when Dialer returns Conn for v6 address
	expectIP := net.ParseIP("2001:db8::42")
	df = dialFixture{[]*net.IP{&expectIP}}
	result, err = d.getHostDefaultAddr()
	if !expectIP.Equal(result) || err != nil {
		t.Errorf("getHostDefaultAddr = %s, %s; want 2001:db8::42, nil",
			result, err)
	}
	// Assert we get v4 address when Dialer does not return Conn for v6
	// addreass but returns Conn for v4 address
	expectIP = net.ParseIP("192.0.2.42")
	df = dialFixture{[]*net.IP{nil, &expectIP}}
	result, err = d.getHostDefaultAddr()
	if !expectIP.Equal(result) || err != nil {
		t.Errorf("getHostDefaultAddr = %s, %s; want 192.0.2.42, nil",
			result, err)
	}
}

func TestGetHostDefaultAddr(t *testing.T) {
	var result net.IP
	var err error

	// We have no control over what result we would get on a test executor
	// but we can validate that the operation is successful and provides
	// the expected type of data.
	result, err = GetHostDefaultAddr()
	if err != nil {
		t.Errorf("GetHostDefaultAddr = %s, %s; want ANY, nil",
			result, err)
	}
}
