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

func timeoutReached(err error) bool {
	var terr net.Error
	return errors.As(err, &terr) && terr.Timeout()
}

// discoverBCast attempts to discover Calibre instances using its broadcast method
func discoverSmartBCast() ([]ConnectionInfo, error) {
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
				match := msgRegex.FindSubmatch(reply)
				if match != nil {
					fullStr, nameStr, wirelessPort := string(match[0]), string(match[1]), string(match[3])
					if _, exists := replies[fullStr]; !exists {
						port, _ := strconv.Atoi(wirelessPort)
						ci = append(ci, ConnectionInfo{Host: host, Name: nameStr, TCPPort: port})
						replies[fullStr] = struct{}{}
					}
				}
			}
			if timeoutReached(err) {
				break
			}
		}
		instances <- ci
		close(instances)
	}()
	for i := 0; i < 3; i++ {
		for _, p := range bcastPorts {
			a, _ := net.ResolveUDPAddr("udp", fmt.Sprintf("255.255.255.255:%d", p))
			pc.WriteTo([]byte("UNCaGED"), a)
			time.Sleep(50 * time.Millisecond)
		}
	}
	return <-instances, nil
}

// DiscoverSmartDevice Calibre smart device instances on the local network
func DiscoverSmartDevice() ([]ConnectionInfo, error) {
	// TODO: Try and get mDNS (Bonjour) working
	return discoverSmartBCast()
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
