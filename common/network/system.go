package network

import (
	"context"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DefaultDialTimeout bounds how long a dial to the backend (or SOCKS server)
// may take. Without it, an unreachable or silently dropping backend can pin
// connection goroutines and file descriptors for minutes until the OS TCP
// timeout kicks in, which exhausts resources and causes latency spikes and
// connection resets under load.
const DefaultDialTimeout = 10 * time.Second

var SystemDialer = &systemOutbound{
	Dialer: net.Dialer{
		Timeout: DefaultDialTimeout,
	},
}

func NewSystemDialer(options *OutboundSocketOptions) Dialer {
	if options == nil {
		return SystemDialer
	}

	out := &systemOutbound{
		Dialer: net.Dialer{
			Timeout: DefaultDialTimeout,
			Control: NewDialerControlFromOptions(options),
		},
	}
	SetDialerTCPKeepAlive(&out.Dialer, options.KeepAliveConfig())
	if options.SendThrough != "" {
		out.Dialer.LocalAddr = &net.TCPAddr{IP: net.ParseIP(options.SendThrough)}
	}
	if options.MultiPathTCP {
		SetDialerMultiPathTCP(&out.Dialer, true)
	}

	return out
}

type ControlFunc = func(network string, address string, c syscall.RawConn) error

type systemOutbound struct {
	net.Dialer
}

func (o *systemOutbound) DialTCPContext(ctx context.Context, network, address string) (*net.TCPConn, error) {
	conn, err := o.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return conn.(*net.TCPConn), nil
}

func (o *systemOutbound) DialContextWithSRV(ctx context.Context, network string, address string, serviceName string) (net.Conn, error) {
	var proto string
	switch network {
	case "tcp", "tcp4", "tcp6":
		proto = "tcp"
	case "udp", "udp4", "udp6":
		proto = "udp"
	default:
		return nil, net.UnknownNetworkError(network)
	}
	domain, _, _ := strings.Cut(address, ":")
	_, srvResults, _ := net.DefaultResolver.LookupSRV(ctx, serviceName, proto, domain)
	for _, result := range srvResults {
		conn, err := o.DialContext(ctx, network, net.JoinHostPort(result.Target, strconv.Itoa(int(result.Port))))
		if err == nil {
			return conn, nil
		}
	}
	return o.DialContext(ctx, network, address)
}
