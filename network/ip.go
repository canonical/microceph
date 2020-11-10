package network

import (
	"net"
)

// An interface with methods for determining information about a hosts point of
// view into a network.
type hostNetInfo interface {
	getHostDefaultAddr() (net.IP, error)
}

// Information used to find our directon out of a host
type hostNetDirector struct {
	// addresses used to determine IP of interface connected to router
	routableAddrs []string
	// net.Dial compatible dialer to use
	dialer func(string, string) (net.Conn, error)
}

func (d hostNetDirector) getHostDefaultAddr() (net.IP, error) {
	var err error
	for _, addr := range d.routableAddrs {
		// Note that a UDP Dial only prepares a socket and does not
		// actually send anything on the wire.
		var conn net.Conn
		conn, err = d.dialer("udp", addr)
		if err != nil {
			// If a host does not have a any route that fulfills
			// the current address or address family Dial will
			// error out with network unreachable. Iterate over the
			// addresses provided in the received data until we
			// succeed and fall back to return error at the end of
			// the method.
			continue
		}
		defer conn.Close()
		localAddr := conn.LocalAddr().(*net.UDPAddr)
		if localAddr != nil {
			return localAddr.IP, nil
		}
		// the Go compiler is pedantic about return at end of functions
		// so instead of returning here we break and utilize the
		// fallback return at the end of the function
		break
	}
	// if we get here we were unable to determine the default address
	return nil, err
}

// Determine the IPv6 or IPv4 address used for outbound communication from host
//
// We do this by preparing a socket for outbound UDP communicaton to an address
// that should not be directly connected to any host and read back the local
// address from the prepared socket.
func GetHostDefaultAddr() (net.IP, error) {
	// Addresses chosen from IANA documentation ranges RFC3849, RFC5737
	// Port number is deliberately invalid as we will never actuall send
	// data on the wire.
	var h hostNetInfo = hostNetDirector{
		[]string{"[2001:db8::]:0", "192.0.2.0:0"},
		net.Dial,
	}
	return h.getHostDefaultAddr()
}
