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

	writeToConnC  chan *GotaFrame
	readFromConnC chan *GotaFrame

	readPool  chan chan *GotaFrame
	writePool chan chan *GotaFrame
}

func NewTunnelManager(rc chan *GotaFrame, wc chan *GotaFrame) *TunnelManager {
	rp := make(chan chan *GotaFrame)
	wp := make(chan chan *GotaFrame)
	quit := make(chan struct{})
	mu := &sync.Mutex{}
	pool := make([]*TunnelTransport, 0)
	return &TunnelManager{
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
		case c := <-tm.readPool:
			c <- <-tm.readFromConnC
		}
	}
}

func (tm *TunnelManager) writeDispatch() {
	for {
		select {
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
	quit     chan struct{}

	mu      sync.Locker
	stopped bool

	timeout int

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
	return &TunnelTransport{
		clientID: c,

		readPool:  rp,
		writePool: wp,

		readChannel:  rc,
		writeChannel: wc,
	}
}

// ReadFromTunnel reads from tunnel and send to connection manager
func (t *TunnelTransport) readFromPeerTunnel() {
	defer t.close()
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

		log.Debugf("TT: Received data frame header from server: %s", gf)

		if gf.IsControl() {
			// TODO
			switch gf.SeqNum {
			case TMHeartBeatPingSeq:
				go t.sendHeartBeatResponse()
			case TMHeartBeatPongSeq:
				log.Info("TT: Received Hearbeat Pong")
			}
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
	defer t.close()
	defer Recover()
	tick := time.NewTicker(time.Second * TMHeartBeatTickerSecond)

	for {
		// register the current worker into the worker queue.
		t.readPool <- t.readChannel
	CONTINUE:
		select {
		case gf := <-t.readChannel:
			// we have received a write request.
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

			// the heartbeat response is only send the worker itself, so skip to register into queue
			if gf.IsControl() && gf.SeqNum == TMHeartBeatPongSeq {
				goto CONTINUE
			}

		case <-time.After(time.Second * TMHeartBeatSecond):
			if t.sendHeartBeatRequest() != nil {
				log.Error("TT: Send heartbeat failed, stop this worker")
				break
			}
			log.Info("TT: Sent Hearbeat Ping")
			goto CONTINUE

		case <-tick.C:
			if t.sendHeartBeatRequest() != nil {
				log.Error("TT: Send heartbeat failed, stop this worker")
				break
			}
			log.Info("TT: Sent Hearbeat Ping")
			goto CONTINUE

		case <-t.quit:
			// received a signal to stop
			return
		}
	}
}

func (t *TunnelTransport) sendHeartBeatRequest() error {
	return WriteNBytes(t.rw, HeaderLength, TMHeartBeatPingBytes)
}

func (t *TunnelTransport) sendHeartBeatResponse() {
	t.readChannel <- TMHeartBeatPongGotaFrame
}

// Start method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (t *TunnelTransport) Start() {
	go t.readFromPeerTunnel()
	go t.writeToPeerTunnel()
}

// Stop signals the worker to stop listening for work requests.
func (t *TunnelTransport) Stop() {
	// close a channel will trigger all reading from the channel to return immediately
	close(t.quit)
}

func (t *TunnelTransport) Stopped() bool {
	return t.stopped
}

func (t *TunnelTransport) close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	defer Recover()

	if !t.stopped {
		t.stopped = true
		close(t.readChannel)
		close(t.writeChannel)
		return t.rw.Close()
	}
	return nil
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
