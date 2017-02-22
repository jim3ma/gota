package gota

import (
	"bytes"
	"errors"
	"fmt"
	log "github.com/Sirupsen/logrus"
	"golang.org/x/net/proxy"
	"io"
	"net"
	"net/url"
	"sync"
	"time"
)

type TunnelActiveConfig struct {
	LocalAddr  *net.TCPAddr
	RemoteAddr *net.TCPAddr
	Proxy      *url.URL
}

type TunnelPassiveConfig struct {
	*net.TCPAddr
}

type TunnelManager struct {
	// only uesd for active mode
	clientID uint32
	mode     int
	config   interface{}

	quit    chan struct{}
	stopped bool
	mutex   sync.Locker

	ttPool []*TunnelTransport

	newCCIDChannel chan CCID

	writeToConnC  chan *GotaFrame
	readFromConnC chan *GotaFrame

	readPool  map[uint32]chan chan *GotaFrame
	writePool map[uint32]chan chan *GotaFrame
}

// NewTunnelManager returns a new TunnelManager
// rc is used for reading fron connection manager
// wc is used for writing to connection manager
func NewTunnelManager(rc chan *GotaFrame, wc chan *GotaFrame) *TunnelManager {
	rp := make(map[uint32]chan chan *GotaFrame)
	wp := make(map[uint32]chan chan *GotaFrame)
	quit := make(chan struct{})
	mu := &sync.Mutex{}
	pool := make([]*TunnelTransport, 0)
	return &TunnelManager{
		newCCIDChannel: nil,

		readFromConnC: rc,
		writeToConnC:  wc,

		readPool:  rp,
		writePool: wp,

		quit:  quit,
		mutex: mu,

		ttPool: pool,
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
		return TMErrUnsupportConfig
	}
	tm.config = config
	return nil
}

func (tm *TunnelManager) SetCCIDChannel(c chan CCID) {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	tm.newCCIDChannel = c
}

func (tm *TunnelManager) Start() {
	if tm.mode == ActiveMode {
		tm.startActiveMode()
	} else {
		tm.startPassiveMode()
	}
	go tm.readDispatch()
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
		// TODO graceful shutdown?
		select {
		case <-tm.quit:
			log.Info("TM: Received quit signal")
			listener.Close()
		case <-restart:
		}
	}()

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			select {
			// quit signal
			case <-tm.quit:
				return
			default:
				// TODO if encount network error, restart
				log.Errorf("TM: Accept Connection Error: %s", err)
				log.Info("TM: Restart Tunnel with Configuration: %+v", config)
				go tm.listenAndServe(config)

				// close the routine for listening graceful shutdown
				close(restart)
			}
		}

		log.Infof("TM: Accept Connection from: %s", conn.RemoteAddr())
		// TODO set Client ID

		// Authenticate start
		username := "gota"
		password := "gota"

		request, err := ReadGotaFrame(conn)
		if err != nil {
			log.Error("TM: Received gota frame header error: %s, stop auth", err)
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

			log.Error("TM: Auth credentail error, close connection")
			conn.Close()
			continue
		}

		err = WriteNBytes(conn, len(TMTunnelAuthOKBytes), TMTunnelAuthOKBytes)
		if err != nil {
			log.Error("TM: Write gota frame header error, close connection")
			conn.Close()
			continue
		}
		log.Infof("TM: User %s, authenticate success, client ID: %d", username, request.clientID)
		// Authenticate end

		client := request.clientID

		if _, ok := tm.readPool[client]; ! ok {
			tm.readPool[client] = make(chan chan *GotaFrame)
		}
		if _, ok := tm.writePool[client]; ! ok {
			tm.writePool[client] = make(chan chan *GotaFrame)
		}

		t := NewTunnelTransport(tm.writePool[client], tm.readPool[client], conn, client)
		t.SetCCIDChannel(tm.newCCIDChannel)
		tm.ttPool = append(tm.ttPool, t)

		//go tm.readDispatchForClient(client)
		go tm.writeDispatchForClient(client)
		go t.Start()
	}
}

func (tm *TunnelManager) connectAndServe(config TunnelActiveConfig, client uint32) {
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
		log.Errorf("TM: Connect remote end error: %s", err)
		return
	}

	log.Infof("TM: Connected remote: %s", conn.RemoteAddr())

	// Authenticate start

	// TODO hard code credential
	username := "gota"
	password := "gota"

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

	if response.SeqNum != TMTunnelAuthOKSeq {
		log.Error("TM: Authenticate error, please check the credential")
		return
	}
	log.Infof("TM: Authenticate success, client ID: %d", client)
	// Authenticate end

	if _, ok := tm.readPool[client]; ! ok {
		tm.readPool[client] = make(chan chan *GotaFrame)
	}
	if _, ok := tm.writePool[client]; ! ok {
		tm.writePool[client] = make(chan chan *GotaFrame)
	}

	t := NewTunnelTransport(tm.writePool[client], tm.readPool[client], conn, client)
	tm.ttPool = append(tm.ttPool, t)

	//go tm.readDispatchForClient(client)
	go tm.writeDispatchForClient(client)
	t.Start()
}

func (tm *TunnelManager) readDispatch() {
	for {
		gf := <-tm.readFromConnC
		log.Debugf("TM: Received frame from CM: %s", gf)
		client := gf.clientID
		<-tm.readPool[client] <- gf
	}
}

func (tm *TunnelManager) readDispatchForClient(client uint32) {
	pool := tm.readPool[client]
	for {
		select {
		// for TT read
		case c := <-pool:
			gf := <-tm.readFromConnC
			log.Debugf("TM: Received frame from CM: %s", gf)
			c <- gf
		}
	}
}

func (tm *TunnelManager) writeDispatchForClient(client uint32) {
	pool := tm.writePool[client]

	if tm.clientID != 0 {
		log.Infof("TM: Launch Write Dispatch for Client: %d, Write Pool: %d", client, &pool)
	}
	for {
		select {
		// for TT write
		case c := <-pool:
			gf := <-c
			log.Debugf("TM: Send frame to CM: %s", gf)
			tm.writeToConnC <- gf
		}
	}
}

func (tm *TunnelManager) stopAllTunnelTransport() {
	for _, v := range tm.ttPool {
		v.Stop()
	}
}

type TunnelTransport struct {
	clientID uint32

	quit chan struct{}

	mutex        sync.Locker
	writeStopped bool
	readStopped  bool

	timeout int

	newCCIDChannel chan CCID

	readPool    chan<- chan *GotaFrame
	readChannel chan *GotaFrame

	writePool    chan<- chan *GotaFrame
	writeChannel chan *GotaFrame

	rw io.ReadWriteCloser
}

func NewTunnelTransport(wp, rp chan<- chan *GotaFrame, rw io.ReadWriteCloser, clientID ...uint32) *TunnelTransport {
	var c uint32
	if clientID != nil {
		c = clientID[0]
	} else {
		c = 0
	}

	rc := make(chan *GotaFrame)
	wc := make(chan *GotaFrame)

	quit := make(chan struct{})
	return &TunnelTransport{
		quit:  quit,
		mutex: &sync.Mutex{},

		clientID: c,

		newCCIDChannel: nil,

		readPool:  rp,
		writePool: wp,

		readChannel:  rc,
		writeChannel: wc,

		rw: rw,
	}
}

func (t *TunnelTransport) SetCCIDChannel(c chan CCID) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.newCCIDChannel = c
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
	}()
	defer Recover()
	for {
		gf, err := ReadGotaFrame(t.rw)
		if err != nil {
			log.Error("TT: Received gota frame error, stop this worker")
			return
		}
		gf.clientID = t.clientID

		log.Debugf("TT: Received gota frame header from peer: %s", gf)

		if gf.IsControl() {
			log.Debug("TT: Process Control Signal")
			switch gf.SeqNum {
			case TMHeartBeatPingSeq:
				go t.sendHeartBeatResponse()
			case TMHeartBeatPongSeq:
				log.Info("TT: Received Hearbeat Pong")

			case TMCreateConnSeq:
				log.Debug("TT: Received Create Connection Signal")
				t.newCCIDChannel <- NewCCID(gf.clientID, gf.ConnID)
			case TMCreateConnOKSeq:
				// TODO optimize
				log.Debug("TT: Received Create Connection OK Signal")
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
			continue
		}

		// register the current worker into the worker queue.
		log.Debug("TT: Try to register into the write worker queue")
		t.writePool <- t.writeChannel
		log.Debug("TT: Registered into the write worker queue")

		select {
		case t.writeChannel <- gf:
		case <-t.quit:
			return
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
	}()
	defer Recover()
	tick := time.NewTicker(time.Second * TMHeartBeatTickerSecond)

	log.Info("TT: Start to forward Gota Frame to peer tunnel")
	for {
		select {
		// register the current worker into the worker queue.
		case t.readPool <- t.readChannel:
			log.Debugf("TT: Registered into the read worker queue: %d", &t.readPool)
		case gf := <-t.readChannel:
			// we have received a write request.
			log.Debugf("TT: Send data frame header to peer: %s", gf)
			rawBytes, err := gf.MarshalBinary()
			if err != nil && nil != HeaderOnly {
				log.Errorf("TT: Marshal GotaFrame error: %+v, skip", err)
				continue
			}
			err = WriteNBytes(t.rw, len(rawBytes), rawBytes)
			if err != nil && nil != io.EOF {
				log.Error("TT: Write gota frame error, stop this worker")
				break
			}

		case <-time.After(time.Second * TMHeartBeatSecond):
			if t.sendHeartBeatRequest() != nil {
				log.Error("TT: Send heartbeat failed, stop this worker")
				break
			}
			log.Info("TT: Sent Hearbeat Ping")
		case <-tick.C:
			if t.sendHeartBeatRequest() != nil {
				log.Error("TT: Send heartbeat failed, stop this worker")
				break
			}
			log.Info("TT: Sent Hearbeat Ping")
		case <-t.quit:
			// TODO if the read channel already registered to the read pool

			// received stop tunnel signal
			// just workaround
			if t.readStopped {
				return
			}

			// received a signal to stop
			t.sendCloseTunnelRequest()
			return
		}
	}
}

func (t *TunnelTransport) sendHeartBeatRequest() error {
	return WriteNBytes(t.rw, HeaderLength, TMHeartBeatPingBytes)
}

func (t *TunnelTransport) sendHeartBeatResponse() {
	t.readChannel <- TMHeartBeatPongGotaFrame
	log.Info("TT: Sent Hearbeat Pong")
}

func (t *TunnelTransport) sendCloseTunnelRequest() error {
	err := WriteNBytes(t.rw, HeaderLength, TMCloseTunnelBytes)
	if err != nil {
		return err
	}
	return nil
}

func (t *TunnelTransport) sendCloseTunnelResponse() {
	err := WriteNBytes(t.rw, HeaderLength, TMCloseTunnelOKBytes)
	if err != nil {
		log.Errorf("TT: Sent Close Tunnel response error: %s", err)
	}
	log.Info("TT: Sent Close Tunnel response")
	t.readStopped = true
	close(t.quit)
}

// Start method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (t *TunnelTransport) Start() {
	go t.readFromPeerTunnel()
	go t.writeToPeerTunnel()
}

// Stop signals the worker to stop listening for work requests.
func (t *TunnelTransport) Stop() {
	t.mutex.Lock()
	defer t.mutex.Unlock()

	if t.readStopped && t.writeStopped {
		return
	}
	// close a channel will trigger all reading from the channel to return immediately
	close(t.quit)
	timeout := 0
	for {
		if timeout < TMHeartBeatSecond {
			time.Sleep(time.Second)
			timeout++
		} else {
			if t.readStopped && t.writeStopped {
				return
			} else {
				t.rw.Close()
				return
			}
		}
	}
}

func (t *TunnelTransport) Stopped() bool {
	return t.readStopped && t.writeStopped
}

const (
	ActiveMode = iota
	PassiveMode
)

var modeMap map[string]int

var TMErrUnsupportConfig = errors.New("Unsupport configuration")

func init() {
	modeMap = make(map[string]int)
	modeMap["Active"] = 0
	modeMap["ActiveMode"] = 0

	modeMap["Passive"] = 1
	modeMap["PassiveMode"] = 1
}
