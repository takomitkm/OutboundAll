// dial.go -- TCP probe helper (shared by supervisor and controller).
package supervisor

import (
	"net"
	"time"
)

// dialTimeout connects to addr ("host:port") with a timeout; nil on failure.
func dialTimeout(addr string, timeout time.Duration) net.Conn {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil
	}
	return c
}
