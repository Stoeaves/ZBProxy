package minecraft

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/common"
	"github.com/layou233/zbproxy/v3/common/access"
	"github.com/layou233/zbproxy/v3/common/buf"
	"github.com/layou233/zbproxy/v3/common/bufio"
	"github.com/layou233/zbproxy/v3/common/mcprotocol"
	"github.com/layou233/zbproxy/v3/common/network"
	"github.com/layou233/zbproxy/v3/common/network/socks"
	"github.com/layou233/zbproxy/v3/common/proxyprotocol"
	"github.com/layou233/zbproxy/v3/common/set"
	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/version"

	"github.com/phuslu/log"
	"github.com/zhangyunhao116/fastrand"
)

var minecraftSRV = &adapter.SRVMetadata{ServiceName: "minecraft"}

// bufferedServerConn wraps a net.Conn with a bytes.Buffer so that
// already-read data can be replayed before reading from the real connection.
type bufferedServerConn struct {
	net.Conn
	buf *bytes.Buffer
}

func (b *bufferedServerConn) Read(p []byte) (int, error) {
	if b.buf.Len() > 0 {
		return b.buf.Read(p)
	}
	return b.Conn.Read(p)
}

type Outbound struct {
	access sync.RWMutex
	logger *log.Logger
	config *config.Outbound
	router adapter.Router
	dialer network.Dialer

	hostnameAccessLists []set.StringSet
	nameAccessLists     []set.StringSet
	onlineCount         atomic.Int32
}

var (
	_ adapter.Outbound = (*Outbound)(nil)
	_ network.Dialer   = (*Outbound)(nil)
)

func NewOutbound(logger *log.Logger, newConfig *config.Outbound) (*Outbound, error) {
	if newConfig.Minecraft == nil {
		return nil, errors.New("not Minecraft outbound config")
	}
	outbound := &Outbound{
		logger: logger,
		config: newConfig,
	}
	return outbound, nil
}

func (o *Outbound) Name() (name string) {
	o.access.RLock()
	if o.config != nil {
		name = o.config.Name
	}
	o.access.RUnlock()
	return
}

func (o *Outbound) OnlineCount() int32 {
	return o.onlineCount.Load()
}

// Config returns the outbound configuration.
// The returned pointer is only valid while no Reload is in progress.
func (o *Outbound) Config() *config.Outbound {
	return o.config
}

func (o *Outbound) PostInitialize(router adapter.Router, provider adapter.RouteResourceProvider) error {
	var err error
	if o.config.Minecraft.HostnameAccess.Mode != access.DefaultMode {
		o.hostnameAccessLists, err = provider.FindListsByTag(o.config.Minecraft.HostnameAccess.ListTags)
		if err != nil {
			return common.Cause("load access control lists: ", err)
		}
	}
	if o.config.Minecraft.NameAccess.Mode == "search" {
		// Search mode: API is called per-connection, nothing to pre-fetch
	} else if o.config.Minecraft.NameAccess.Mode != access.DefaultMode {
		o.nameAccessLists, err = provider.FindListsByTag(o.config.Minecraft.NameAccess.ListTags)
		if err != nil {
			return common.Cause("load access control lists: ", err)
		}
	}
	if o.config.Minecraft.MotdFavicon == "{DEFAULT_MOTD}" {
		o.config.Minecraft.MotdFavicon = defaultMOTD
	}
	o.config.Minecraft.MotdDescription = strings.NewReplacer(
		"{INFO}", "ZBProxy "+version.Version,
		"{NAME}", o.config.Name,
		"{HOST}", o.config.TargetAddress,
		"{PORT}", strconv.Itoa(int(o.config.TargetPort)),
	).Replace(o.config.Minecraft.MotdDescription)

	if samples := o.config.Minecraft.OnlineCount.Sample; samples != nil {
		var convertedSamples []playerSample
		switch samples := samples.(type) {
		case map[string]any:
			convertedSamples = make([]playerSample, 0, len(samples))
			for uuid, name := range samples {
				convertedSamples = append(convertedSamples, playerSample{
					Name: name.(string),
					ID:   uuid,
				})
			}

		case []any:
			convertedSamples = make([]playerSample, 0, len(samples))
			var u [16]byte
			var dst [36]byte
			for i, sample := range samples {
				// generate random UUID with ZBProxy signature
				fastrand.Read(u[:])
				u[0] = byte(i)
				u[1] = '$'
				u[2] = 'Z'
				u[3] = 'B'
				u[4] = '$'

				// marshal UUID string
				const hexTable = "0123456789abcdef"
				dst[8] = '-'
				dst[13] = '-'
				dst[18] = '-'
				dst[23] = '-'
				for i, x := range [16]byte{
					0, 2, 4, 6,
					9, 11,
					14, 16,
					19, 21,
					24, 26, 28, 30, 32, 34,
				} {
					c := u[i]
					dst[x] = hexTable[c>>4]
					dst[x+1] = hexTable[c&0x0F]
				}

				convertedSamples = append(convertedSamples, playerSample{
					Name: sample.(string),
					ID:   string(dst[:]),
				})
			}

		default:
			return fmt.Errorf("unknown player samples type: %T", samples)
		}
		o.config.Minecraft.OnlineCount.Sample = convertedSamples
	}

	if o.config.Dialer != "" {
		if o.config.SocketOptions != nil {
			return errors.New("socket options are not available when dialer is specified")
		}
		o.dialer, err = provider.FindOutboundByName(o.config.Dialer)
		if err != nil {
			return err
		}
	} else {
		o.dialer = network.NewSystemDialer(o.config.SocketOptions)
	}
	switch o.config.ProxyProtocolVersion {
	case proxyprotocol.VersionUnspecified,
		proxyprotocol.Version1,
		proxyprotocol.Version2:
	default:
		return fmt.Errorf("invalid proxy protocol version: %v", o.config.ProxyProtocolVersion)
	}
	switch o.config.ProxyOptions.Type {
	case "socks", "socks5", "socks4a", "socks4":
		o.dialer = &socks.Client{
			Dialer:  o.dialer,
			Version: o.config.ProxyOptions.Type,
			Network: o.config.ProxyOptions.Network,
			Address: o.config.ProxyOptions.Address,
		}
	}
	o.router = router
	return nil
}

func (o *Outbound) Reload(options adapter.OutboundReloadOptions) error {
	o.access.Lock()
	defer o.access.Unlock()
	o.config = options.Config
	o.hostnameAccessLists = nil
	o.nameAccessLists = nil
	return o.PostInitialize(o.router, &options)
}

func (o *Outbound) connectServer(ctx context.Context, metadata *adapter.Metadata) (net.Conn, error) {
	if metadata.DestinationHostname == "" {
		metadata.DestinationHostname = o.config.TargetAddress
	}
	if metadata.DestinationPort == 0 {
		metadata.DestinationPort = o.config.TargetPort
	}
	destinationAddress := net.JoinHostPort(metadata.DestinationHostname, strconv.FormatUint(uint64(metadata.DestinationPort), 10))
	if !o.config.Minecraft.IgnoreSRVRedirect {
		metadata.SRV = minecraftSRV
	}
	conn, err := adapter.DialContextWithMetadata(o.dialer, ctx, "tcp", destinationAddress, metadata)
	if err != nil {
		return nil, err
	}
	if o.config.ProxyProtocolVersion != proxyprotocol.VersionUnspecified {
		var localAddress netip.AddrPort
		localAddress, err = netip.ParseAddrPort(conn.LocalAddr().String())
		if err != nil {
			conn.Close()
			return nil, common.Cause("failed to parse local address: ", err)
		}
		err = (&proxyprotocol.Header{
			Version:           uint8(o.config.ProxyProtocolVersion),
			Command:           proxyprotocol.CommandProxy,
			TransportProtocol: proxyprotocol.TransportProtocolStream | proxyprotocol.AddressFamilyByAddr(metadata.SourceAddress.Addr()),
			SourceAddress:     metadata.SourceAddress,
		}).WriteHeader(conn, localAddress)
		if err != nil {
			conn.Close()
			return nil, common.Cause("failed to write PROXY protocol header: ", err)
		}
	}
	return conn, nil
}

func (o *Outbound) InjectConnection(ctx context.Context, conn *bufio.CachedConn, metadata *adapter.Metadata) error {
	if metadata.Minecraft == nil {
		return errors.New("require Minecraft metadata")
	}
	if !metadata.Minecraft.Valid() {
		return errors.New("invalid Minecraft protocol")
	}
	o.access.RLock()

	if o.config.Minecraft.HostnameAccess.Mode != access.DefaultMode {
		hostnameClean := metadata.Minecraft.CleanOriginDestination()
		if o.config.Minecraft.HostnameAccess.LowerCase {
			hostnameClean = strings.ToLower(hostnameClean)
		}
		if !access.Check(o.hostnameAccessLists, o.config.Minecraft.HostnameAccess.Mode, hostnameClean) {
			err := common.Cause("hostname "+o.config.Minecraft.HostnameAccess.Mode+
				" mode, request="+url.QueryEscape(hostnameClean)+": ", access.ErrRejected)
			o.access.RUnlock()
			conn.Conn.(*net.TCPConn).SetLinger(0)
			return err
		}
	}
	if metadata.Minecraft.SniffPosition >= 0 {
		conn.Rewind(metadata.Minecraft.SniffPosition)
	}
	switch metadata.Minecraft.NextState {
	case mcprotocol.IntentStatus:
		// skip Status Request packet
		_, err := conn.Peek(2)
		if err != nil {
			o.access.RUnlock()
			return common.Cause("skip status request: ", err)
		}
		if o.config.Minecraft.MotdFavicon == "" && o.config.Minecraft.MotdDescription == "" {
			// directly proxy MOTD from server
			var remoteConn net.Conn
			remoteConn, err = o.connectServer(ctx, metadata)
			if err != nil {
				o.access.RUnlock()
				return common.Cause("request remote MOTD: ", err)
			}
			//remoteConn.(*net.TCPConn).SetLinger(0) // for some reason
			if metadata.Minecraft.RewrittenDestination == "" {
				metadata.Minecraft.RewrittenDestination = metadata.Minecraft.CleanOriginDestination()
			}
			if metadata.Minecraft.RewrittenPort == 0 {
				metadata.Minecraft.RewrittenPort = metadata.Minecraft.OriginPort
			}
			buffer := buf.New()
			buffer.Reset(mcprotocol.MaxVarIntLen)

			hostname := metadata.Minecraft.RewrittenDestination
			if o.config.Minecraft.EnableHostnameRewrite {
				hostname = o.config.Minecraft.RewrittenHostname
				if hostname == "" {
					hostname = o.config.TargetAddress
				}
			} else if hostname == "" {
				hostname = metadata.Minecraft.CleanOriginDestination()
			}
			if !o.config.Minecraft.IgnoreFMLSuffix && metadata.Minecraft.IsFML() {
				hostname += "\x00" + metadata.Minecraft.FMLMarkup()
			}
			port := metadata.Minecraft.RewrittenPort
			if port <= 0 {
				port = metadata.Minecraft.OriginPort
			}
			// construct handshake packet
			buffer.WriteByte(0) // Server bound : Handshake
			mcprotocol.VarInt(metadata.Minecraft.ProtocolVersion).WriteToBuffer(buffer)
			mcprotocol.WriteString(buffer, hostname)
			binary.BigEndian.PutUint16(buffer.Extend(2), port)
			buffer.WriteByte(mcprotocol.IntentStatus)
			mcprotocol.AppendPacketLength(buffer, buffer.Len())
			// construct status packet
			buffer.WriteByte(1)
			buffer.WriteByte(0)
			// send 2 packets in 1 write call
			_, err = remoteConn.Write(buffer.Bytes())
			buffer.Release()
			if err != nil {
				o.access.RUnlock()
				return common.Cause("request remote MOTD: ", err)
			}
			o.access.RUnlock()
			return bufio.CopyConn(remoteConn, conn)
		} else {
			motd := generateMOTD(metadata.Minecraft.ProtocolVersion, o.config, &o.onlineCount)
			buffer := buf.New()
			buffer.Reset(mcprotocol.MaxVarIntLen)
			buffer.WriteByte(0) // Client bound : Status Response
			mcprotocol.VarInt(len(motd)).WriteToBuffer(buffer)
			clientMC := mcprotocol.Conn{
				Reader: conn,
				Writer: common.UnwrapWriter(conn), // unwrap to make writev syscall possible
				Conn:   conn,
			}
			err = clientMC.WriteVectorizedPacket(buffer, motd)
			if err != nil {
				o.access.RUnlock()
				buffer.Release()
				return common.Cause("respond MOTD: ", err)
			}

			switch o.config.Minecraft.PingMode {
			case pingModeDisconnect:
				// do nothing and disconnect
			case pingMode0ms:
				buffer.WriteByte(1)  // Client bound : Ping Response
				buffer.WriteZeroN(8) // size of int64 timestamp
				err = clientMC.WritePacket(buffer)
				buffer.Release()
				if err != nil {
					o.access.RUnlock()
					return common.Cause("respond 0ms ping: ", err)
				}
			default:
				err = clientMC.ReadLimitedPacket(buffer, 9)
				if err != nil {
					o.access.RUnlock()
					buffer.Release()
					return common.Cause("read ping request: ", err)
				}
				err = clientMC.WritePacket(buffer)
				buffer.Release()
				if err != nil {
					o.access.RUnlock()
					return common.Cause("respond ping request: ", err)
				}
			}
			o.logger.Info().Str("id", metadata.ConnectionID).Str("outbound", o.config.Name).Msg("Responded MOTD")
			o.access.RUnlock()
			return nil
		}

	case mcprotocol.IntentLogin:
		buffer := buf.New()
		buffer.Reset(mcprotocol.MaxVarIntLen)
		if o.config.Minecraft.NameAccess.Mode == "search" {
			if o.config.Minecraft.NameAccess.SearchParam == "planId" {
				name := metadata.Minecraft.PlayerName
				uuidStr := fmt.Sprintf("%x", metadata.Minecraft.UUID)
				// When client does not provide UUID (pre-1.19 or hasUUID=false),
				// fetch it from Mojang API using the player name.
				if uuidStr == "00000000000000000000000000000000" {
					o.logger.Warn().Str("player", name).Msg("Player UUID not provided by client, fetching from Mojang")
					var mojangErr error
					uuidStr, mojangErr = fetchUUIDFromMojang(o.logger, name)
					if mojangErr != nil {
						o.access.RUnlock()
						buffer.Release()
						return common.Cause("fetch UUID from Mojang: ", mojangErr)
					}
				}
				allowed, nameUpdated, needBindQQ, apiErr, err := queryPlayerSubscription(o.logger, name, uuidStr, o.config.Minecraft.NameAccess.PlanId)
				if err != nil || apiErr != "" {
					o.access.RUnlock()
					msg, marshalErr := generateUnknownErrorMessage(o.config, metadata.Minecraft.PlayerName, apiErr).MarshalJSON()
					if marshalErr != nil {
						buffer.Release()
						return common.Cause("generate unknown error message: ", marshalErr)
					}
					buffer.WriteByte(0)
					mcprotocol.VarInt(len(msg)).WriteToBuffer(buffer)
					writeErr := mcprotocol.Conn{Writer: common.UnwrapWriter(conn)}.WriteVectorizedPacket(buffer, msg)
					buffer.Release()
					if writeErr != nil {
						return common.Cause("send unknown error kick packet: ", writeErr)
					}
					o.logger.Warn().Str("id", metadata.ConnectionID).Str("outbound", o.config.Name).
						Str("player", metadata.Minecraft.PlayerName).Str("api_error", apiErr).Err(err).
						Msg("Kicked by subscription API error")
					conn.Conn.(*net.TCPConn).SetLinger(10)
					return nil
				}
				if !allowed {
					var msg []byte
					switch {
					case nameUpdated:
						msg, err = generatePlayerNameUpdated(o.config, metadata.Minecraft.PlayerName).MarshalJSON()
					case needBindQQ:
						msg, err = generatePlayerNotBoundQQ(o.config, metadata.Minecraft.PlayerName).MarshalJSON()
					default:
						msg, err = generateKickMessage(o.config, metadata.Minecraft.PlayerName).MarshalJSON()
					}
					if err != nil {
						o.access.RUnlock()
						buffer.Release()
						return common.Cause("generate kick message: ", err)
					}
					buffer.WriteByte(0)
					mcprotocol.VarInt(len(msg)).WriteToBuffer(buffer)
					err = mcprotocol.Conn{Writer: common.UnwrapWriter(conn)}.WriteVectorizedPacket(buffer, msg)
					if err != nil {
						o.access.RUnlock()
						buffer.Release()
						return common.Cause("send kick packet: ", err)
					}
					o.logger.Warn().Str("id", metadata.ConnectionID).Str("outbound", o.config.Name).
						Str("player", metadata.Minecraft.PlayerName).Msg("Kicked by subscription check")
					o.access.RUnlock()
					conn.Conn.(*net.TCPConn).SetLinger(10)
					buffer.Release()
					return nil
				}
			}
		} else if o.config.Minecraft.NameAccess.Mode != access.DefaultMode {
			name := metadata.Minecraft.PlayerName
			if o.config.Minecraft.NameAccess.LowerCase {
				name = strings.ToLower(metadata.Minecraft.PlayerName)
			}
			if !access.Check(o.nameAccessLists, o.config.Minecraft.NameAccess.Mode, name) {
				msg, err := generateKickMessage(o.config, metadata.Minecraft.PlayerName).MarshalJSON()
				if err != nil { // almost impossible
					o.access.RUnlock()
					buffer.Release()
					return common.Cause("generate kick message: ", err)
				}
				buffer.WriteByte(0) // Client bound : Disconnect (login)
				mcprotocol.VarInt(len(msg)).WriteToBuffer(buffer)
				err = mcprotocol.Conn{Writer: common.UnwrapWriter(conn)}.WriteVectorizedPacket(buffer, msg)
				if err != nil {
					o.access.RUnlock()
					buffer.Release()
					return common.Cause("send kick packet: ", err)
				}
				o.logger.Warn().Str("id", metadata.ConnectionID).Str("outbound", o.config.Name).
					Str("player", metadata.Minecraft.PlayerName).Msg("Kicked by name access control")
				o.access.RUnlock()
				conn.Conn.(*net.TCPConn).SetLinger(10)
				buffer.Release()
				return nil
			}
		}
		if o.config.Minecraft.OnlineCount.EnableMaxLimit &&
			o.config.Minecraft.OnlineCount.Max <= o.onlineCount.Load() {
			msg, err := generatePlayerNumberLimitExceededMessage(o.config, metadata.Minecraft.PlayerName).MarshalJSON()
			if err != nil {
				o.access.RUnlock()
				buffer.Release()
				return common.Cause("generate player number limit exceeded packet: ", err)
			}
			buffer.WriteByte(0)
			mcprotocol.VarInt(len(msg)).WriteToBuffer(buffer)
			err = mcprotocol.Conn{Writer: common.UnwrapWriter(conn)}.WriteVectorizedPacket(buffer, msg)
			if err != nil {
				o.access.RUnlock()
				buffer.Release()
				return common.Cause("send player number limit exceeded packet: ", err)
			}
			o.logger.Warn().Str("id", metadata.ConnectionID).Str("outbound", o.config.Name).
				Str("player", metadata.Minecraft.PlayerName).Msg("Kicked by player number limiter")
			o.access.RUnlock()
			conn.Conn.(*net.TCPConn).SetLinger(10)
			buffer.Release()
			return nil
		}

		serverConn, err := o.connectServer(ctx, metadata)
		if err != nil {
			o.access.RUnlock()
			buffer.Release()
			return common.Cause("connect server: ", err)
		}
		hostname := metadata.Minecraft.RewrittenDestination
		if o.config.Minecraft.EnableHostnameRewrite {
			hostname = o.config.Minecraft.RewrittenHostname
			if hostname == "" {
				hostname = o.config.TargetAddress
			}
		} else if hostname == "" {
			hostname = metadata.Minecraft.CleanOriginDestination()
		}
		if !o.config.Minecraft.IgnoreFMLSuffix && metadata.Minecraft.IsFML() {
			hostname += "\x00" + metadata.Minecraft.FMLMarkup()
		}
		port := metadata.Minecraft.RewrittenPort
		if port <= 0 {
			port = metadata.Minecraft.OriginPort
		}
		// construct handshake packet
		buffer.WriteByte(0) // Server bound : Handshake
		mcprotocol.VarInt(metadata.Minecraft.ProtocolVersion).WriteToBuffer(buffer)
		mcprotocol.WriteString(buffer, hostname)
		binary.BigEndian.PutUint16(buffer.Extend(2), port)
		buffer.WriteByte(mcprotocol.IntentLogin)
		mcprotocol.AppendPacketLength(buffer, buffer.Len())
		// write handshake and login packet
		cache := conn.Cache()
		vector := net.Buffers{buffer.Bytes(), cache.Bytes()}
		_, err = vector.WriteTo(serverConn)
		buffer.Release()
		if err != nil {
			o.access.RUnlock()
			serverConn.Close()
			return common.Cause("server handshake: ", err)
		}
		cache.Advance(cache.Len()) // all written
		o.logger.Info().Str("id", metadata.ConnectionID).Str("outbound", o.config.Name).
			Str("player", metadata.Minecraft.PlayerName).Msg("Created Minecraft connection")
		outboundConfig := o.config // capture before releasing lock
		o.access.RUnlock()

		// Read the first packet from server to detect ban disconnect
		firstPacketRaw, readPacketErr := readServerFirstPacket(serverConn)
		if readPacketErr == nil {
			// Parse packet ID from the packet content (after VarInt length prefix)
			packetID, _, idErr := mcprotocol.ReadVarIntFrom(bytes.NewReader(firstPacketRaw.content))
			if idErr == nil && packetID == 0x00 { // Login Disconnect
				o.handleServerDisconnect(firstPacketRaw, outboundConfig, metadata, serverConn, conn)
				o.onlineCount.Add(-1)
				return nil
			}
			// Not a disconnect — wrap serverConn so CopyConn replays the already-read packet
			serverConn = &bufferedServerConn{
				Conn: serverConn,
				buf:  bytes.NewBuffer(append(firstPacketRaw.lengthBytes, firstPacketRaw.content...)),
			}
		}
		o.onlineCount.Add(1)
		err = bufio.CopyConn(serverConn, conn)
		o.onlineCount.Add(-1)
		return err

	case mcprotocol.IntentTransfer:
		// TODO: Minecraft transfer support
		o.access.RUnlock()
		conn.Conn.(*net.TCPConn).SetLinger(0)
		return conn.Close()

	default:
		o.access.RUnlock()
		return fmt.Errorf("unknown intent: %d", metadata.Minecraft.NextState)
	}
}

func (o *Outbound) DialContext(context.Context, string, string) (net.Conn, error) {
	return nil, adapter.ErrInjectionRequired
}

// uuidSystemAPIResponse is the JSON structure returned by the UUID system API.
type uuidSystemAPIResponse struct {
	Code  int    `json:"code"`
	Error string `json:"error,omitempty"` // error type when code is not 200
	Data  *struct {
		ExpiredTime string `json:"expiredTime"` // Unix timestamp in milliseconds (string)
	} `json:"data,omitempty"`
}

// mojangProfileResponse is the JSON structure returned by the Mojang API.
type mojangProfileResponse struct {
	ID   string `json:"id"`   // UUID without dashes
	Name string `json:"name"` // player name
}

// fetchUUIDFromMojang fetches the player's UUID from the Mojang API.
// Returns the UUID string without dashes (32 hex chars).
func fetchUUIDFromMojang(logger *log.Logger, name string) (string, error) {
	apiURL := "https://api.mojang.com/users/profiles/minecraft/" + url.PathEscape(name)

	logger.Info().Str("name", name).Msg("Fetching UUID from Mojang API")

	resp, err := http.DefaultClient.Get(apiURL)
	if err != nil {
		return "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNoContent {
		return "", fmt.Errorf("player not found at Mojang: %s", name)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("unexpected HTTP status %s: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	logger.Info().Str("body", string(body)).Msg("Mojang API response")

	var profile mojangProfileResponse
	err = json.Unmarshal(body, &profile)
	if err != nil {
		return "", fmt.Errorf("unmarshal json: %w", err)
	}

	if profile.ID == "" {
		return "", fmt.Errorf("Mojang returned empty UUID for %s", name)
	}

	return profile.ID, nil
}

// serverFirstPacket holds the raw bytes of the first packet read from a server connection.
type serverFirstPacket struct {
	lengthBytes []byte // Raw VarInt length prefix bytes
	content     []byte // Packet content (packet ID + data)
}

// readServerFirstPacket reads exactly one Minecraft packet from conn and returns its raw bytes.
func readServerFirstPacket(conn net.Conn) (*serverFirstPacket, error) {
	var lengthBuf []byte
	for i := 0; i < 5; i++ {
		b := make([]byte, 1)
		if _, err := io.ReadFull(conn, b); err != nil {
			return nil, err
		}
		lengthBuf = append(lengthBuf, b[0])
		if b[0]&0x80 == 0 {
			break
		}
	}
	length, _, err := mcprotocol.ReadVarIntFrom(bytes.NewReader(lengthBuf))
	if err != nil {
		return nil, err
	}
	content := make([]byte, length)
	if _, err = io.ReadFull(conn, content); err != nil {
		return nil, err
	}
	return &serverFirstPacket{lengthBytes: lengthBuf, content: content}, nil
}

// handleServerDisconnect processes a server disconnect (packet ID 0x00).
// If the disconnect message indicates a ban, it calls the ban API and sends
// a custom kick message to the client. Otherwise it forwards the original packet.
func (o *Outbound) handleServerDisconnect(pkt *serverFirstPacket, config *config.Outbound, metadata *adapter.Metadata, serverConn net.Conn, clientConn net.Conn) {
	// Parse packet ID and skip it
	packetID, idLen, _ := mcprotocol.ReadVarIntFrom(bytes.NewReader(pkt.content))
	if packetID != 0x00 {
		// Not a disconnect, shouldn't reach here
		clientConn.Close()
		serverConn.Close()
		return
	}
	remaining := pkt.content[idLen:]

	// Parse the JSON Chat message in the disconnect packet
	msgLen, msgLenRead, _ := mcprotocol.ReadVarIntFrom(bytes.NewReader(remaining))
	var msg mcprotocol.Message
	isBan := false
	n := int(msgLen)
	nRead := int(msgLenRead)
	if n > 0 && n <= len(remaining)-nRead {
		msgBytes := remaining[nRead : nRead+n]
		if err := json.Unmarshal(msgBytes, &msg); err == nil && isBanMessage(&msg) {
			isBan = true
			// Send Telegram notification asynchronously
			uuidStr := fmt.Sprintf("%x", metadata.Minecraft.UUID)
			go o.banPlayer(metadata.Minecraft.PlayerName, uuidStr)
			// Send custom ban message to client
			msg = generateBanKickMessage(config, metadata.Minecraft.PlayerName)
		}
	}

	if isBan {
		// Send modified disconnect packet
		msgJSON, _ := msg.MarshalJSON()
		newBuf := buf.New()
		newBuf.Reset(mcprotocol.MaxVarIntLen)
		mcprotocol.VarInt(0x00).WriteToBuffer(newBuf)
		mcprotocol.WriteString(newBuf, string(msgJSON))
		clientMC := mcprotocol.Conn{Writer: common.UnwrapWriter(clientConn)}
		_ = clientMC.WritePacket(newBuf)
		newBuf.Release()
	} else {
		// Forward original disconnect as-is
		raw := append(pkt.lengthBytes, pkt.content...)
		_, _ = clientConn.Write(raw)
	}
	serverConn.Close()
	clientConn.Close()
}

// banPlayer sends a Telegram Bot notification when a player is banned.
// Fetches UUID from Mojang API for pre-1.19 clients that don't provide it.
func (o *Outbound) banPlayer(name, uuid string) {
	if uuid == "00000000000000000000000000000000" {
		var err error
		uuid, err = fetchUUIDFromMojang(o.logger, name)
		if err != nil {
			o.logger.Warn().Err(err).Str("player", name).Msg("Telegram Bot: failed to fetch UUID from Mojang, skipped notification")
			return
		}
	}
	text := fmt.Sprintf("🚫 玩家 %s 已被封禁\nUUID: %s", name, uuid)
	apiURL := fmt.Sprintf("https://api.telegram.org/bot8755019953:AAEGsKaRQV5Wrim_w4kjGn6yrkHhTy7hHCY/sendMessage?chat_id=5212251919&text=%s",
		url.QueryEscape(text))
	resp, err := http.DefaultClient.Post(apiURL, "", nil)
	if err != nil {
		o.logger.Warn().Err(err).Str("player", name).Str("uuid", uuid).Msg("Telegram Bot: failed to send notification")
		return
	}
	resp.Body.Close()
	o.logger.Info().Str("player", name).Str("uuid", uuid).Int("status", resp.StatusCode).Msg("Telegram Bot: ban notification sent")
}

// isBanMessage checks whether a Minecraft Chat message indicates a ban.
func isBanMessage(msg *mcprotocol.Message) bool {
	raw, err := json.Marshal(msg)
	if err != nil {
		return false
	}
	lower := strings.ToLower(string(raw))
	return strings.Contains(lower, "banned") ||
		strings.Contains(lower, "ban") ||
		strings.Contains(lower, "suspended") ||
		strings.Contains(lower, "your account")
}

// queryPlayerSubscription queries the UUID system API to check a player's
// subscription status. Returns:
//   - allowed: true if the player is allowed to connect
//   - nameUpdated: if not allowed, true means name was updated (use generatePlayerNameUpdated)
//   - needBindQQ: if not allowed and not nameUpdated, true means {code:403} (use generatePlayerNotBoundQQ),
//     false means subscription not found or expired (use generateKickMessage)
func queryPlayerSubscription(logger *log.Logger, name, uuid, planId string) (allowed bool, nameUpdated bool, needBindQQ bool, apiErr string, err error) {
	apiURL := fmt.Sprintf(
		"https://stiper.im/api/admin/uuidSystem?name=%s&uuid=%s&planId=%s",
		url.QueryEscape(name), url.QueryEscape(uuid), url.QueryEscape(planId),
	)

	logger.Info().Str("name", name).Str("uuid", uuid).Str("planId", planId).Msg("Querying UUID system API")

	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return false, false, false, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer U9CNadUgyE3e0Msm93TDnYyukCn6t9mx7zcNVVeV2fyC0w2vM4")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, false, false, "", fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		logger.Warn().Str("status", resp.Status).Str("body", string(body)).Msg("UUID system API returned non-200")
		return false, false, false, "", fmt.Errorf("unexpected HTTP status %s: %s", resp.Status, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, false, false, "", fmt.Errorf("read body: %w", err)
	}

	logger.Info().Str("body", string(body)).Msg("UUID system API response")

	var apiResp uuidSystemAPIResponse
	err = json.Unmarshal(body, &apiResp)
	if err != nil {
		return false, false, false, "", fmt.Errorf("unmarshal json: %w", err)
	}

	switch apiResp.Code {
	case 200:
		if apiResp.Data == nil {
			return false, false, false, "", fmt.Errorf("missing data in API response")
		}
		expiredMs, err := strconv.ParseInt(apiResp.Data.ExpiredTime, 10, 64)
		if err != nil {
			return false, false, false, "", fmt.Errorf("parse expiredTime: %w", err)
		}
		nowUnix := time.Now().Unix()
		expiredUnix := expiredMs / 1000
		logger.Info().Int64("now", nowUnix).Int64("expiredTime", expiredUnix).Msg("Subscription expiry check")
		if nowUnix >= expiredUnix {
			return false, false, false, "", nil // expired → kick with generateKickMessage
		}
		return true, false, false, "", nil // valid → allow

	case 201:
		return false, true, false, apiResp.Error, nil // name updated → kick with generatePlayerNameUpdated

	case 403:
		return false, false, true, apiResp.Error, nil // not bound QQ → kick with generatePlayerNotBoundQQ

	case 404:
		return false, false, false, apiResp.Error, nil // not found → kick with generateUnknownErrorMessage

	default:
		if apiResp.Error != "" {
			return false, false, false, apiResp.Error, nil // API returned error code with error type
		}
		return false, false, false, "", fmt.Errorf("API returned unexpected code: %d", apiResp.Code)
	}
}
