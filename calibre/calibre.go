package calibre

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"
)

// ConnectionInfo contains the connection information for a Calibre instance
type ConnectionInfo struct {
	Host    string `json:"host"`
	TCPPort int    `json:"port"`
	Name    string `json:"name"`
}

// Logger is an interface to provide logging functionality
type Logger interface {
	// LogPrintf logs non-critical warnings
	LogPrintf(format string, a ...interface{})
}

func timeoutReached(err error) bool {
	var terr net.Error
	return errors.As(err, &terr) && terr.Timeout()
}

// discoverBCast attempts to discover Calibre instances using its broadcast method
func discoverSmartBCast(calLog Logger) ([]ConnectionInfo, error) {
	// Most calibre instances will respond to the first port in this list, as that
	// is what it tries to bins to first, but all of them should be checked for
	// completeness sake.
	bcastPorts := []int{54982, 48123, 39001, 44044, 59678}
	pc, err := net.ListenPacket("udp", "0.0.0.0:0")
	if err != nil {
		return nil, fmt.Errorf("discoverBCast: error opening PacketConn: %w", err)
	}
	instances := make(chan []ConnectionInfo)
	go func() {
		replies := make(map[string]struct{})
		ci := make([]ConnectionInfo, 0)
		calibreReply := make([]byte, 512)
		pc.SetReadDeadline(time.Now().Add(1000 * time.Millisecond))
		msgRegex := regexp.MustCompile(`calibre wireless device client \(on ([^\)]+)\);(\d{2,5}),(\d{2,5})`)
		for {
			bytesRead, addr, err := pc.ReadFrom(calibreReply)
			if bytesRead > 0 {
				host, _, _ := net.SplitHostPort(addr.String())
				reply := calibreReply[:bytesRead]
				calLog.LogPrintf("discoverSmartBCast: received reply from %s", host)
				match := msgRegex.FindSubmatch(reply)
				if match != nil {
					fullStr, nameStr, wirelessPort := string(match[0]), string(match[1]), string(match[3])
					calLog.LogPrintf("discoverSmartBCast: name: %s port: %s", nameStr, wirelessPort)
					if _, exists := replies[fullStr]; !exists {
						port, _ := strconv.Atoi(wirelessPort)
						ci = append(ci, ConnectionInfo{Host: host, Name: nameStr, TCPPort: port})
						replies[fullStr] = struct{}{}
					}
				}
			}
			if timeoutReached(err) {
				calLog.LogPrintf("discoverSmartBCast: read timed out")
				break
			}
		}
		instances <- ci
		close(instances)
	}()
	discoverPacket := []byte("UNCaGED")
	for i := 0; i < 3; i++ {
		for _, p := range bcastPorts {
			a, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("255.255.255.255:%d", p))
			pc.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
			n, err := pc.WriteTo(discoverPacket, a)
			if n != len(discoverPacket) || err != nil {
				if timeoutReached(err) {
					calLog.LogPrintf("discoverSmartBCast: write timed out")
					continue
				}
				return nil, fmt.Errorf("discoverSmartBCast: wrote %d of %d bytes: %w", n, len(discoverPacket), err)
			}
			calLog.LogPrintf("discoverSmartBCast: wrote 'hello' packet to port %d", p)
			time.Sleep(50 * time.Millisecond)
		}
	}
	return <-instances, nil
}

// DiscoverSmartDevice Calibre smart device instances on the local network
func DiscoverSmartDevice(calLog Logger) ([]ConnectionInfo, error) {
	// TODO: Try and get mDNS (Bonjour) working

	// Attempt discovery up to three times to try and compensate for poor network conditions
	for i := 0; i < 3; i++ {
		ci, err := discoverSmartBCast(calLog)
		if len(ci) > 0 {
			return ci, err
		} else if err != nil {
			return nil, err
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, nil
}

// Connect to a Calibre instance, either on local or remote networks
func Connect(host string, port int) (net.Conn, error) {
	conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil, fmt.Errorf("Connect: error dialling Calibre: %w", err)
	}
	return conn, nil
}

// Connect to this Calibre instance
func (c *ConnectionInfo) Connect() (net.Conn, error) {
	return Connect(c.Host, c.TCPPort)
}
