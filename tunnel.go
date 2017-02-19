package gota

import (
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
	mode   int
	config interface{}

	quit    chan struct{}
	stopped bool
	mutex   sync.Locker

	ttPool []*TunnelTransport

	newCCIDChannel chan CCID

	writeToConnC  chan *GotaFrame
	readFromConnC chan *GotaFrame

	readPool  chan chan *GotaFrame
	writePool chan chan *GotaFrame
}

// NewTunnelManager returns a new TunnelManager
// rc is used for reading fron connection manager
// wc is used for writing to connection manager
func NewTunnelManager(rc chan *GotaFrame, wc chan *GotaFrame) *TunnelManager {
	rp := make(chan chan *GotaFrame)
	wp := make(chan chan *GotaFrame)
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
	go tm.writeDispatch()
}

func (tm *TunnelManager) Stop() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()
	if tm.stopped {
		return
	}
	tm.stopped = true
	close(tm.quit)
	close(tm.readPool)
	close(tm.writePool)
	tm.stopAllTunnelTransport()
}

func (tm *TunnelManager) startActiveMode() {
	config, ok := tm.config.([]TunnelActiveConfig)
	if !ok {
		return
	}
	for _, c := range config {
		go tm.connectAndServe(c)
	}
}

func (tm *TunnelManager) startPassiveMode() {
	config, ok := tm.config.([]TunnelPassiveConfig)
	if !ok {
		return
	}
	for _, c := range config {
		go tm.listenAndServe(c)
	}
}

func (tm *TunnelManager) listenAndServe(config TunnelPassiveConfig) {
	listener, err := net.ListenTCP("tcp", config.TCPAddr)
	if err != nil {
		panic(err)
	}

	go func() {
		// TODO graceful shutdown?
		select {
		case <-tm.quit:
			log.Info("TM: Received quit signal")
			listener.Close()
		}
	}()

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Errorf("TM: Accept Connection Error: %s", err)
		}

		// TODO authenticate and set Client ID

		t := NewTunnelTransport(tm.writePool, tm.readPool, conn)
		tm.ttPool = append(tm.ttPool, t)
		go t.Start()
	}
}

func (tm *TunnelManager) connectAndServe(config TunnelActiveConfig) {
	var conn net.Conn
	var err error

	log.Infof("TM: Tunnel Active Configuration: %+v", config)

	if config.Proxy != nil {
		raddr := fmt.Sprintf("%s:%d", config.RemoteAddr.IP, config.RemoteAddr.Port)

		var dialer proxy.Dialer
		switch config.Proxy.Scheme {
		case "https":
			log.Infof("TM: Connect remoet using HTTPS proxy")
			dialer, _ = proxy.FromURL(config.Proxy, HTTPSDialer)
		case "http":
			log.Infof("TM: Connect remoet using HTTP proxy")
			dialer, _ = proxy.FromURL(config.Proxy, Direct)
		case "socks5":
			log.Infof("TM: Connect remoet using SOCKS5 proxy")
			dialer, _ = proxy.FromURL(config.Proxy, Direct)
		default:
			log.Errorf("TM: Unsupport proxy: %+v", config.Proxy)
		}
		conn, err = dialer.Dial("tcp", raddr)
	} else {
		conn, err = net.DialTCP("tcp", config.LocalAddr, config.RemoteAddr)
	}

	if err != nil {
		log.Errorf("TM: Connect remote end error: %s", err)
	}

	// TODO authenticate and set Client ID

	t := NewTunnelTransport(tm.writePool, tm.readPool, conn)
	tm.ttPool = append(tm.ttPool, t)
	go t.Start()
}

func (tm *TunnelManager) readDispatch() {
	for {
		select {
		// for TT read
		case c := <-tm.readPool:
			c <- <-tm.readFromConnC
		}
	}
}

func (tm *TunnelManager) writeDispatch() {
	for {
		select {
		// for TT write
		case c := <-tm.writePool:
			tm.writeToConnC <- <-c
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
		header, err := ReadNBytes(t.rw, HeaderLength)
		if err != nil {
			log.Error("TT: Received gota frame header error, stop this worker")
			return
		}

		var gf GotaFrame
		err = gf.UnmarshalBinary(header)
		if err != nil && err != HeaderOnly {
			log.Error("TT: Error GotaFrame: %+v", header)
			return
		}

		log.Debugf("TT: Received data frame header from peer: %s", gf)

		if gf.IsControl() {
			// TODO
			switch gf.SeqNum {
			case TMHeartBeatPingSeq:
				go t.sendHeartBeatResponse()
			case TMHeartBeatPongSeq:
				log.Info("TT: Received Hearbeat Pong")
			case TMCreateConnSeq:
				t.newCCIDChannel <- NewCCID(gf.clientID, gf.ConnID)
			case TMCloseTunnelSeq:
				log.Info("TT: Received Close Tunnel Request, Stop Read!")
				go t.sendCloseTunnelResponse()
				return
			case TMCloseTunnelOKSeq:
				log.Info("TT: Received Close Tunnel Response, Stop Read!")
				return
			}
			continue
		}

		payload, err := ReadNBytes(t.rw, gf.Length)
		if err != nil {
			log.Error("TT: Received gota frame error, stop this worker")
			return
		}
		gf.Payload = payload

		// register the current worker into the worker queue.
		t.writePool <- t.writeChannel

		select {
		case t.writeChannel <- &gf:
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
