package gota

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync"
	"time"

	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/proxy"
)

type TunnelAuthCredential struct {
	UserName string
	Password string
}

type TunnelActiveConfig struct {
	LocalAddr  *net.TCPAddr
	RemoteAddr *net.TCPAddr
	Proxy      *url.URL
}

type TunnelPassiveConfig struct {
	*net.TCPAddr
}

type ClientID = uint32

type TunnelManager struct {
	auth     *TunnelAuthCredential
	whiteIPs []*net.IPNet

	// only used for active mode
	clientID ClientID
	mode     int
	config   interface{}

	quit    chan struct{}
	stopped bool
	mutex   sync.Locker

	ttPool []*TunnelTransport

	newCCIDChannel chan CCID

	cleanUpAllConnCh   chan ClientID
	cleanUpReadPoolCh  chan ClientID
	cleanUpWritePoolCh chan ClientID

	writeToConnC  chan *GotaFrame
	readFromConnC chan *GotaFrame

	poolLock  sync.RWMutex
	readPool  map[ClientID]chan chan *GotaFrame
	writePool map[ClientID]chan chan *GotaFrame

	// TODO reconnect gota server
	reConnect     bool
	reConnectCh   chan interface{}
	reConnectTime time.Duration
}

// NewTunnelManager returns a new TunnelManager
// rc is used for reading from connection manager
// wc is used for writing to connection manager
func NewTunnelManager(rc chan *GotaFrame, wc chan *GotaFrame, options ...func(*TunnelManager) error) *TunnelManager {
	rp := make(map[ClientID]chan chan *GotaFrame)
	wp := make(map[ClientID]chan chan *GotaFrame)
	quit := make(chan struct{})
	mu := &sync.Mutex{}
	pool := make([]*TunnelTransport, 0)
	cleanRead := make(chan ClientID)
	cleanWrite := make(chan ClientID)
	tm := &TunnelManager{
		newCCIDChannel:     nil,
		cleanUpReadPoolCh:  cleanRead,
		cleanUpWritePoolCh: cleanWrite,

		readFromConnC: rc,
		writeToConnC:  wc,

		poolLock:  sync.RWMutex{},
		readPool:  rp,
		writePool: wp,

		quit:  quit,
		mutex: mu,

		ttPool: pool,
	}
	for _, option := range options {
		err := option(tm)
		if err != nil {
			log.Errorf("TM: Error for option: %s", err)
		}
	}
	return tm
}

func TMAuth(auth *TunnelAuthCredential) func(tm *TunnelManager) error {
	return func(tm *TunnelManager) error {
		tm.auth = auth
		return nil
	}
}

func (tm *TunnelManager) Mode() int {
	return tm.mode
}

func (tm *TunnelManager) SetConfig(config interface{}) error {
	switch c := config.(type) {
	case TunnelActiveConfig:
		tm.mode = ActiveMode
		tm.config = make([]TunnelActiveConfig, 1)
		if v, ok := tm.config.([]TunnelActiveConfig); ok {
			v[0] = c
		}
		return nil
	case []TunnelActiveConfig:
		tm.mode = ActiveMode
	case TunnelPassiveConfig:
		tm.mode = PassiveMode
		tm.config = make([]TunnelPassiveConfig, 1)
		if v, ok := tm.config.([]TunnelPassiveConfig); ok {
			v[0] = c
		}
		return nil
	case []TunnelPassiveConfig:
		tm.mode = PassiveMode
	default:
		return TMErrUnsupportedConfig
	}
	tm.config = config
	return nil
}

func (tm *TunnelManager) SetWhiteIPs(s []string) {
	ips := make([]*net.IPNet, len(s))
	for i, v := range s {
		_, ips[i], _ = net.ParseCIDR(v)
	}
	tm.whiteIPs = ips
}

func (tm *TunnelManager) SetCCIDChannel(c chan CCID) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	tm.newCCIDChannel = c
}

func (tm *TunnelManager) SetClientID(c ClientID) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	tm.clientID = c
}

func (tm *TunnelManager) Start() {
	if tm.mode == ActiveMode {
		tm.startActiveMode()
	} else {
		tm.startPassiveMode()
	}
	go tm.readDispatch()
	go tm.cleanUpReadPool()
	go tm.cleanUpWritePool()
	//go tm.writeDispatch()
}

func (tm *TunnelManager) Stop() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	if tm.stopped {
		return
	}
	tm.stopped = true
	close(tm.quit)
	// TODO close all read and write pool
	//close(tm.readPool)
	//close(tm.writePool)
	tm.stopAllTunnelTransport()
	close(tm.cleanUpReadPoolCh)
	close(tm.cleanUpWritePoolCh)
}

func (tm *TunnelManager) startActiveMode() {
	config, ok := tm.config.([]TunnelActiveConfig)
	if !ok {
		return
	}
	log.Info("TM: Work in Active Mode")
	for _, c := range config {
		go tm.connectAndServe(c, tm.clientID)
	}
}

func (tm *TunnelManager) startPassiveMode() {
	config, ok := tm.config.([]TunnelPassiveConfig)
	if !ok {
		return
	}
	log.Info("TM: Work in Passive Mode")
	for _, c := range config {
		go tm.listenAndServe(c)
	}
}

func (tm *TunnelManager) listenAndServe(config TunnelPassiveConfig) {
	log.Infof("TM: Tunnel Passive Configuration: %+v", config)

	listener, err := net.ListenTCP("tcp", config.TCPAddr)
	if err != nil {
		panic(err)
	}

	restart := make(chan struct{})

	go func() {
		select {
		case <-tm.quit:
			log.Infof("TM: Received quit signal, listener info: %s", listener.Addr())
			listener.Close()
		case <-restart:
		}
	}()

	// Authenticate start
	var username string
	var password string
	if tm.auth == nil {
		username = "gota"
		password = "gota"
	} else {
		username = tm.auth.UserName
		password = tm.auth.Password
	}

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			select {
			// quit signal
			case <-tm.quit:
				return
			default:
				// TODO if encounter network error, restart
				log.Errorf("TM: Accept Connection Error: %s", err)
				log.Info("TM: Restart Tunnel with Configuration: %+v", config)

				// close the routine for listening graceful shutdown
				close(restart)
				listener.Close()
				go tm.listenAndServe(config)
			}
		}

		if len(tm.whiteIPs) != 0 {
			addr := strings.Split(conn.RemoteAddr().String(), ":")
			ip := net.ParseIP(addr[0])
			ok := false
			for _, ipm := range tm.whiteIPs {
				ipx := ip.Mask(ipm.Mask)
				if ipx.Equal(ipm.IP) {
					ok = true
					break
				}
			}
			if !ok {
				log.Debugf("TM: IP Address is not in white list, addr: %s", conn.RemoteAddr())
				conn.Close()
				continue
			}
		}

		log.Infof("TM: Accept Connection from: %s", conn.RemoteAddr())

		request, err := ReadGotaFrame(conn)
		if err != nil {
			log.Errorf("TM: Received gota frame header error: %s, stop auth", err)
			conn.Close()
			continue
		}

		if request.SeqNum != TMTunnelAuthSeq {
			log.Error("TM: Auth header error, close connection")
			conn.Close()
			continue
		}

		ParseClientIDHeaderFromPayload(request)
		if bytes.Compare(
			NewBasicAuthGotaFrame(username, password).Payload,
			request.Payload,
		) != 0 {
			log.Errorf("TM: Authentication credential error, close connection from client: %d", request.clientID)
			WriteNBytes(conn, len(TMTunnelAuthErrBytes), TMTunnelAuthErrBytes)
			conn.Close()
			continue
		}

		err = WriteNBytes(conn, len(TMTunnelAuthOKBytes), TMTunnelAuthOKBytes)
		if err != nil {
			log.Error("TM: Write gota frame header error, close connection")
			conn.Close()
			continue
		}
		log.Infof("TM: User %s, authentication success, client ID: %d", username, request.clientID)
		// Authenticate end

		client := request.clientID

		tm.poolLock.Lock()
		if _, ok := tm.readPool[client]; !ok {
			tm.readPool[client] = make(chan chan *GotaFrame)
		}
		if _, ok := tm.writePool[client]; !ok {
			tm.writePool[client] = make(chan chan *GotaFrame)
		}
		tm.poolLock.Unlock()

		tm.poolLock.RLock()
		t := NewTunnelTransport(config, tm.writePool[client], tm.readPool[client], conn, PassiveMode,
			tm.cleanUpReadPoolCh, tm.cleanUpWritePoolCh, client)
		tm.poolLock.RUnlock()

		t.SetCCIDChannel(tm.newCCIDChannel)
		tm.ttPool = append(tm.ttPool, t)

		go tm.writeDispatch(client)
		go t.Start()
	}
}

func (tm *TunnelManager) connectAndServe(config TunnelActiveConfig, client ClientID) {
	var conn net.Conn
	var err error

	log.Infof("TM: Tunnel Active Configuration: %+v", config)

	if config.Proxy != nil {
		raddr := fmt.Sprintf("%s:%d", config.RemoteAddr.IP, config.RemoteAddr.Port)

		var dialer proxy.Dialer
		switch config.Proxy.Scheme {
		case "https":
			log.Infof("TM: Connect remote using HTTPS proxy")
			dialer, _ = proxy.FromURL(config.Proxy, HTTPSDialer)
		case "http":
			log.Infof("TM: Connect remote using HTTP proxy")
			dialer, _ = proxy.FromURL(config.Proxy, Direct)
		case "socks5":
			log.Infof("TM: Connect remote using SOCKS5 proxy")
			dialer, _ = proxy.FromURL(config.Proxy, Direct)
		default:
			log.Errorf("TM: Unsupport proxy: %+v", config.Proxy)
		}
		conn, err = dialer.Dial("tcp", raddr)
	} else {
		conn, err = net.DialTCP("tcp", config.LocalAddr, config.RemoteAddr)
	}
	// TODO TLS dial

	// TODO retry
	if err != nil {
		log.Fatalf("TM: Connect remote end error: %s", err)
		return
	}

	log.Infof("TM: Connected remote: %s", conn.RemoteAddr())

	// Authenticate start
	var username string
	var password string
	if tm.auth == nil {
		username = "gota"
		password = "gota"
	} else {
		username = tm.auth.UserName
		password = tm.auth.Password
	}

	request := NewBasicAuthGotaFrame(username, password)

	request.clientID = client

	EmbedClientIDHeaderToPayload(request)

	rawBytes, _ := request.MarshalBinary()
	err = WriteNBytes(conn, len(rawBytes), rawBytes)
	if err != nil {
		// TODO retry
		log.Error("TM: Write gota frame error, stop auth")
		return
	}

	response, err := ReadGotaFrame(conn)
	if err != nil {
		// TODO retry
		log.Errorf("TM: Received gota frame header error: %s, stop auth", err)
		return
	}

	log.Debugf("TM: Received authentication response gota frame: %s", response)

	if response.SeqNum != TMTunnelAuthOKSeq {
		log.Error("TM: Authentication error, please check the credential")
		return
	}
	log.Infof("TM: Authentication success, client ID: %d", client)
	// Authenticate end

	tm.poolLock.Lock()
	if _, ok := tm.readPool[client]; !ok {
		tm.readPool[client] = make(chan chan *GotaFrame)
	}
	if _, ok := tm.writePool[client]; !ok {
		tm.writePool[client] = make(chan chan *GotaFrame)
	}
	tm.poolLock.Unlock()

	tm.poolLock.RLock()
	t := NewTunnelTransport(config, tm.writePool[client], tm.readPool[client], conn, ActiveMode,
		tm.cleanUpReadPoolCh, tm.cleanUpWritePoolCh, client)
	tm.poolLock.RUnlock()

	tm.ttPool = append(tm.ttPool, t)

	//go tm.readDispatchForClient(client)
	go tm.writeDispatch(client)
	t.Start()
}

func (tm *TunnelManager) readDispatch() {
	for {
		gf, ok := <-tm.readFromConnC
		if !ok {
			break
		}
		Verbosef("TM: Received frame from CM: %s", gf)
		// multiple client support
		client := gf.clientID

		tm.poolLock.RLock()
		pool, ok := tm.readPool[client]
		tm.poolLock.RUnlock()
		if !ok {
			log.Warnf("TM: Read Pool for client %d didn't exist", client)
			// TODO close all connection for non-exist client id
			go func(id ClientID) {
				tm.cleanUpAllConnCh <- id
			}(client)
			continue
		}

		select {
		case c := <-pool:
			c <- gf
		case <-time.After(10 * time.Millisecond):
			log.Warnf("TM: Timeout for read dispatch, Client ID: %d, Gota Frame: %s, retry in background", client, gf)

			// double check to reduce goroutines
			tm.poolLock.RLock()
			_, ok := tm.readPool[client]
			tm.poolLock.RUnlock()
			// peer TT closed
			if !ok {
				continue
			}
			go func(gf *GotaFrame, client ClientID) {
				tm.poolLock.RLock()
				pool, ok := tm.readPool[client]
				tm.poolLock.RUnlock()
				// peer TT closed
				if !ok {
					return
				}
				select {
				case c := <-pool:
					c <- gf
				case <-time.After(TMHeartBeatSecond * time.Second):
					// TODO broken tunnel, should reconnect to peer when the mode is Active and stop this tunnel when the mode is Passive
					log.Warnf("TM: Timeout for read dispatch, Client ID: %d, Gota Frame: %s, dropped", client, gf)
					return
				}
			}(gf, client)
		}
	}
}

func (tm *TunnelManager) cleanUpReadPool() {
	for cid := range tm.cleanUpReadPoolCh {
		log.Debugf("TM: Clean up read pool for client: %d", cid)
		tm.poolLock.Lock()
		rp, ok := tm.readPool[cid]
		if ok {
			gf := &GotaFrame{}
			<-rp <- gf
			/*
				Loop:
					for {
						select {
						case <-rp <- gf:
						case <-time.After(3 * time.Second):
							break Loop
						}
					}
			*/
			delete(tm.readPool, cid)
		}
		tm.poolLock.Unlock()
	}
}

func (tm *TunnelManager) cleanUpWritePool() {
	for cid := range tm.cleanUpWritePoolCh {
		log.Debugf("TM: Clean up write pool for client: %d", cid)
		tm.poolLock.Lock()
		ch, ok := tm.writePool[cid]
		if ok {
			close(ch)
		}
		delete(tm.writePool, cid)
		tm.poolLock.Unlock()
	}
}

// multiple clients support
func (tm *TunnelManager) writeDispatch(client ClientID) {
	pool := tm.writePool[client]

	if tm.clientID != 0 {
		log.Infof("TM: Launch write dispatch for client: %d, Write Pool: %d", client, &pool)
	}
	for {
		select {
		// for TT write
		case c, ok := <-pool:
			if !ok {
				return
			}
			gf := <-c
			Verbosef("TM: Send frame to CM: %s", gf)
			// TODO send on closed channel
			tm.writeToConnC <- gf
		case <-tm.quit:
			return
		}
	}
}

func (tm *TunnelManager) stopAllTunnelTransport() {
	for _, v := range tm.ttPool {
		v.Stop()
	}
}

type TunnelTransport struct {
	config      interface{}
	reConnect   bool
	reConnectCh chan interface{}

	clientID ClientID

	quit chan struct{}
	mode int

	mutex        sync.Locker
	writeStopped bool
	readStopped  bool

	timeout int

	newCCIDChannel chan CCID

	cleanUpReadPoolCh  chan ClientID
	cleanUpWritePoolCh chan ClientID

	readPool    chan<- chan *GotaFrame
	readChannel chan *GotaFrame

	writePool    chan<- chan *GotaFrame
	writeChannel chan *GotaFrame

	rw io.ReadWriteCloser

	stats *Statistic
}

func NewTunnelTransport(config interface{}, wp, rp chan<- chan *GotaFrame, rw io.ReadWriteCloser, mode int, cleanRead chan ClientID, cleanWrite chan ClientID, clientID ...ClientID) *TunnelTransport {
	var c ClientID
	if clientID != nil {
		c = clientID[0]
	} else {
		c = 0
	}

	rc := make(chan *GotaFrame)
	wc := make(chan *GotaFrame)

	quit := make(chan struct{})

	stats := NewStatistic(TMStatReportSecond)
	return &TunnelTransport{
		quit:  quit,
		mutex: &sync.Mutex{},

		clientID: c,
		mode:     mode,

		newCCIDChannel:     nil,
		cleanUpReadPoolCh:  cleanRead,
		cleanUpWritePoolCh: cleanWrite,

		readPool:  rp,
		writePool: wp,

		readChannel:  rc,
		writeChannel: wc,

		rw:    rw,
		stats: stats,

		config: config,
	}
}

func (t *TunnelTransport) SetCCIDChannel(c chan CCID) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.newCCIDChannel = c
}

func (t *TunnelTransport) logStats() {
	conn, ok := t.rw.(net.Conn)
	if !ok {
		return
	}
	info := fmt.Sprintf("local: %s, remote: %s", conn.LocalAddr(), conn.RemoteAddr())
	for {
		select {
		case <-time.After(TMStatReportSecond * time.Second):
			log.Infof("TT: Statistic - %s - send: %s, receive: %s, total send speed: %s/s, total receive speed: %s/s",
				info, ByteSize(t.stats.SentBytes).String(), ByteSize(t.stats.ReceivedBytes).String(),
				ByteSize(t.stats.SendSpeed()).String(), ByteSize(t.stats.ReceiveSpeed()).String())
			log.Infof("TT: Statistic - %s - current send speed in %ds: %s/s, current receive speed in %ds: %s/s",
				info, TMStatReportSecond, ByteSize(t.stats.SendSpeedSecond(TMStatReportSecond)).String(),
				TMStatReportSecond, ByteSize(t.stats.ReceiveSpeedSecond(TMStatReportSecond)).String())
		case <-t.quit:
			return
		}
	}
}

// ReadFromTunnel reads from tunnel and send to connection manager
func (t *TunnelTransport) readFromPeerTunnel() {
	defer func() {
		close(t.writeChannel)
		t.writeStopped = true

		if c, ok := t.rw.(RWCloseReader); ok {
			c.CloseRead()
		} else {
			t.rw.Close()
		}
		//t.tryShutdown()
		t.cleanUpWritePoolCh <- t.clientID
	}()
	defer Recover()
	for {
		gf, err := ReadGotaFrame(t.rw)
		if err != nil {
			select {
			case <-t.quit:
				return
			default:
				log.Errorf("TT: Received gota frame error, stop this worker, error: %s", err)
				t.Stop()
			}
			return
		}
		gf.clientID = t.clientID

		Verbosef("TT: Received gota frame header from peer: %s", gf)

		if !gf.IsControl() {
			// register the current worker into the worker queue.
			//log.Debug("TT: Try to register into the write worker queue")
			t.writePool <- t.writeChannel
			//log.Debug("TT: Registered into the write worker queue")

			select {
			case t.writeChannel <- gf:
				t.stats.AddReceivedBytes(uint64(gf.Length))
			case <-t.quit:
				return
			}
			continue
		}

		// handler Control Seq
		switch gf.SeqNum {
		case TMHeartBeatPingSeq:
			go t.sendHeartBeatResponse()
		case TMHeartBeatPongSeq:
			log.Info("TT: Received Heartbeat Pong")
		case TMCreateConnSeq:
			log.Debug("TT: Received Create Connection Signal")
			t.newCCIDChannel <- NewCCID(gf.clientID, gf.ConnID)
		case TMCreateFastOpenConnSeq:
			log.Debug("TT: Received Create Fast Open Connection Signal")
			t.newCCIDChannel <- NewCCID(gf.clientID, gf.ConnID)
		case TMCreateConnOKSeq:
			// TODO optimize
			log.Debug("TT: Received Create Connection OK Signal")
			t.writePool <- t.writeChannel
			t.writeChannel <- gf
		case TMCreateConnErrorSeq:
			log.Debug("TT: Received Create Connection Error Signal")
			t.writePool <- t.writeChannel
			t.writeChannel <- gf
		case TMCloseTunnelSeq:
			log.Info("TT: Received Close Tunnel Request, Stop Read!")
			go t.sendCloseTunnelResponse()
			return
		case TMCloseTunnelOKSeq:
			log.Info("TT: Received Close Tunnel Response, Stop Read!")
			return
		case TMCloseConnSeq:
			log.Debug("TT: Received Close Connection Signal")
			t.writePool <- t.writeChannel
			t.writeChannel <- gf
		case TMCloseConnForceSeq:
			log.Debug("TT: Received Force Close Connection Signal")
			t.writePool <- t.writeChannel
			t.writeChannel <- gf
		}
	}
}

// WriteToTunnel reads from connection and send to tunnel
func (t *TunnelTransport) writeToPeerTunnel() {
	defer func() {
		close(t.readChannel)
		t.readStopped = true
		if c, ok := t.rw.(RWCloseWriter); ok {
			c.CloseWrite()
		} else {
			t.rw.Close()
		}
		//t.tryShutdown()
		t.cleanUpReadPoolCh <- t.clientID
	}()
	defer Recover()
	tick := time.NewTicker(time.Second * TMHeartBeatTickerSecond)

	log.Info("TT: Start to forward Gota Frame to peer tunnel")
	sent := false
Loop:
	for {
		select {
		// register the current worker into the worker queue.
		case t.readPool <- t.readChannel:
			sent = true
			continue
			//log.Debugf("TT: Registered into the read worker queue: %d", &t.readPool)
		case gf := <-t.readChannel:
			// we have received a write request.
			Verbosef("TT: Send data frame header to peer: %s", gf)
			t.stats.AddSentBytes(uint64(gf.Length))

			rawBytes, err := gf.MarshalBinary()
			if err != nil && nil != HeaderOnly {
				log.Errorf("TT: Marshal GotaFrame error: %+v, skip", err)
				continue Loop
			}
			err = WriteNBytes(t.rw, len(rawBytes), rawBytes)
			if err != nil && nil != io.EOF {
				log.Error("TT: Write gota frame error, stop this worker")
				break Loop
			}

		case <-time.After(time.Second * TMHeartBeatSecond):
			err := t.sendHeartBeatRequest()
			if err != nil {
				log.Errorf("TT: Send heartbeat failed, stop this worker, error: \"%s\"", err)
				break Loop
			}
			log.Info("TT: Sent Heartbeat Ping")
		case <-tick.C:
			err := t.sendHeartBeatRequest()
			if err != nil {
				log.Errorf("TT: Send heartbeat failed, stop this worker, error: \"%s\"", err)
				break Loop
			}
			log.Info("TT: Sent Heartbeat Ping")
		case <-t.quit:
			go func() {
				t.cleanUpReadPoolCh <- t.clientID
			}()
			// TODO if the read channel already registered to the read pool
			if sent {
				select {
				case <-t.readChannel:
				case <-time.After(3 * time.Second):
				}
			}

			// received a signal to stop
			t.sendCloseTunnelRequest()
			return
		}
	}
}

func (t *TunnelTransport) tryShutdown() {
	if t.Stopped() {
		return
	}
	t.Stop()
}

func (t *TunnelTransport) sendHeartBeatRequest() error {
	return WriteNBytes(t.rw, HeaderLength, TMHeartBeatPingBytes)
}

func (t *TunnelTransport) sendHeartBeatResponse() {
	t.readChannel <- TMHeartBeatPongGotaFrame
	log.Info("TT: Sent Heartbeat Pong")
}

func (t *TunnelTransport) sendCloseTunnelRequest() error {
	log.Info("TT: Sent Close Tunnel request")
	err := WriteNBytes(t.rw, HeaderLength, TMCloseTunnelBytes)
	if err != nil {
		log.Errorf("TT: Sent Close Tunnel request error: %s", err)
	}
	return err
}

func (t *TunnelTransport) sendCloseTunnelResponse() {
	err := WriteNBytes(t.rw, HeaderLength, TMCloseTunnelOKBytes)
	if err != nil {
		log.Errorf("TT: Sent Close Tunnel response error: %s", err)
	}
	log.Info("TT: Sent Close Tunnel response")
	t.readStopped = true
	close(t.quit)
	t.Stop()
}

// Start method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (t *TunnelTransport) Start() {
	go t.readFromPeerTunnel()
	go t.writeToPeerTunnel()
	go t.logStats()
}

// Stop signals the worker to stop listening for work requests.
func (t *TunnelTransport) Stop() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	defer func() {
		if t.mode == ActiveMode {
			ShutdownGota()
		}
	}()

	if t.readStopped && t.writeStopped {
		return
	}

	// close a channel will trigger all reading from the channel to return immediately
	select {
	case <-t.quit:
	default:
		close(t.quit)
	}
	timeout := 0
	for {
		if t.readStopped && t.writeStopped {
			break
		}
		if timeout < TMHeartBeatSecond {
			time.Sleep(time.Second)
			timeout++
		} else {
			t.rw.Close()
			break
		}
	}
	// TODO clean up graceful
	//t.cleanUpReadPoolCh <- t.clientID
}

func (t *TunnelTransport) Stopped() bool {
	return t.readStopped && t.writeStopped
}

const (
	ActiveMode = iota
	PassiveMode
)

var modeMap map[string]int

var TMErrUnsupportedConfig = errors.New("Unsupported configuration")

func init() {
	modeMap = make(map[string]int)
	modeMap["Active"] = 0
	modeMap["ActiveMode"] = 0

	modeMap["Passive"] = 1
	modeMap["PassiveMode"] = 1
}
