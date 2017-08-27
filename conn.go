package gota

import (
	"crypto/rand"
	"encoding/binary"
	log "github.com/Sirupsen/logrus"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

// CCID combines client ID and connection ID into a uint64 for the key of map struct
// +-----------------------+-----------------------+
// |   client id: 32 bit   | connection id: 32 bit |
// +-----------------------+-----------------------+
type CCID uint64

// NewCCID return a new CCID with client id and conn id
func NewCCID(clientID ClientID, connID uint32) (cc CCID) {
	c := uint64(clientID)
	cc = CCID(c<<32 + uint64(connID))
	return
}

// ClientID return client id
func (cc CCID) ClientID() ClientID {
	return uint32(cc >> 32)
}

// ConnID return connection id
func (cc CCID) ConnID() uint32 {
	return uint32(cc & 0x00000000FFFFFFFF)
}

// ConnManager manage connections from listening from local port or connecting to remote server
type ConnManager struct {
	clientID ClientID
	//mode int
	//newConnChannel  chan io.ReadWriteCloser

	newCCIDChannel chan CCID
	cleanUpCHChan  chan CCID

	poolLock        *sync.RWMutex
	connHandlerPool map[CCID]*ConnHandler

	writeToTunnelC  chan *GotaFrame
	readFromTunnelC chan *GotaFrame

	fastOpen bool

	quit    chan struct{}
	stopped bool
	mutex   sync.Locker
}

// NewConnManager returns a new ConnManager,
// We should use the channels of this ConnManager to set up TunnelMangager
func NewConnManager() *ConnManager {
	ncc := make(chan CCID)
	clean := make(chan CCID)
	chPool := make(map[CCID](*ConnHandler))
	wc := make(chan *GotaFrame)
	rc := make(chan *GotaFrame)

	q := make(chan struct{})
	l := &sync.Mutex{}
	ml := &sync.RWMutex{}

	d := make([]byte, 4)
	rand.Read(d)
	clientID := binary.LittleEndian.Uint32(d)

	return &ConnManager{
		clientID: clientID,
		//mode: 0,
		//newConnChannel: nc,

		newCCIDChannel:  ncc,
		cleanUpCHChan:   clean,
		connHandlerPool: chPool,

		writeToTunnelC:  wc,
		readFromTunnelC: rc,

		fastOpen: false,
		quit:     q,
		mutex:    l,
		poolLock: ml,
	}
}

// NewCCIDChannel returns the newCCIDChannel for creating a new connection
func (cm *ConnManager) NewCCIDChannel() chan CCID {
	return cm.newCCIDChannel
}

func (cm *ConnManager) EnableFastOpen() {
	cm.fastOpen = true
}

// WriteToTunnelChannel returns the channel to write to tunnel
func (cm *ConnManager) WriteToTunnelChannel() chan *GotaFrame {
	return cm.writeToTunnelC
}

// ReadFromTunnelChannel returns the channel to read from tunnel
func (cm *ConnManager) ReadFromTunnelChannel() chan *GotaFrame {
	return cm.readFromTunnelC
}

// ListenAndServe listens for addr with port and handles connections coming from them.
// This function can be both client and server
func (cm *ConnManager) ListenAndServe(addr string) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}

	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		log.Fatalf("Listen error: %s", err)
	}

	go cm.cleanUpCHPool()
	go cm.dispatch()

	newConnChannel := make(chan io.ReadWriteCloser)
	go cm.handleNewConn(newConnChannel)

	go func() {
		// TODO graceful shutdown?
		select {
		case <-cm.quit:
			log.Infof("CM: Received quit signal, listener info: %s", listener.Addr())
			close(newConnChannel)
			listener.Close()
		}
	}()
	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			cm.mutex.Lock()
			defer cm.mutex.Unlock()

			// ignore error when call function Stop()
			if cm.stopped {
				return nil
			}

			log.Errorf("CM: Accept Connection Error: %s", err)
			return err
		}
		log.Debugf("CM: Received new connection from %s", conn.RemoteAddr())
		newConnChannel <- conn
	}
}

// Serve just waits request for new connections from tunnel
// This function can be used in both client and server
func (cm *ConnManager) Serve(addr string) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}

	go cm.cleanUpCHPool()
	go cm.dispatch()
	cm.handleNewCCID(tcpAddr)
}

func (cm *ConnManager) handleNewCCID(addr *net.TCPAddr) {
	for cc := range cm.newCCIDChannel {
		log.Debugf("CM: New CCID comes from tunnel, CCID: %d, Client ID: %d, ConnID: %d",
			cc, cc.ClientID(), cc.ConnID())
		go cm.dialAndCreateCH(cc, addr)
	}
}

func (cm *ConnManager) dialAndCreateCH(cc CCID, addr *net.TCPAddr) {
	var conn io.ReadWriteCloser
	var err error
	retry := 0
	log.Debugf("CM: Dial remote for ClientID: %d, ConnID: %d", cc.ClientID(), cc.ConnID())
	for {
		conn, err = net.DialTCP("tcp", nil, addr)
		if err == nil {
			break
		} else {
			retry += 1
			sec := int32(math.Exp2(float64(retry)))
			log.Debugf("CM: Create a new connection to remote error: \"%s\", retry after %d seconds",
				err, sec)
			time.Sleep(time.Second * time.Duration(sec))
		}

		if retry >= MaxRetryTimes {
			log.Debugf("CM: Create a new connection to remote error after retry %d times", retry)

			// TODO enable fast open

			// send response after created the connection error
			resp := &GotaFrame{
				Control:  true,
				ConnID:   cc.ConnID(),
				clientID: cc.ClientID(),
				SeqNum:   TMCreateConnErrorSeq,
				Length:   0,
			}
			cm.writeToTunnelC <- resp

			return
		}
	}
	log.Debugf("CM: Dial remote complete for ClientID: %d, ConnID: %d", cc.ClientID(), cc.ConnID())
	rc := make(chan *GotaFrame, 1)
	ch := &ConnHandler{
		ClientID:        cc.ClientID(),
		ConnID:          cc.ConnID(),
		rw:              conn,
		cleanUpCHChan:   cm.cleanUpCHChan,
		WriteToTunnelC:  cm.writeToTunnelC,
		ReadFromTunnelC: rc,
	}

	go ch.Start()

	cm.poolLock.Lock()
	cm.connHandlerPool[cc] = ch
	cm.poolLock.Unlock()

	// TODO enable fast open
	if !cm.fastOpen {
		// send response after created the connection
		resp := &GotaFrame{
			Control:  true,
			ConnID:   cc.ConnID(),
			clientID: cc.ClientID(),
			SeqNum:   TMCreateConnOKSeq,
			Length:   0,
		}
		cm.writeToTunnelC <- resp
	}

}

func (cm *ConnManager) handleNewConn(newChannel chan io.ReadWriteCloser) {
	var cid uint32 = 0
	for c := range newChannel {
		// create and start a new ConnHandler with a new connection id, than append to cm.connHandlerPool
		log.Debugf("CM: new connection, id: %d", cid)
		rc := make(chan *GotaFrame, 1)

		mu := &sync.Mutex{}
		ch := &ConnHandler{
			ClientID:        cm.clientID,
			ConnID:          cid,
			rw:              c,
			cleanUpCHChan:   cm.cleanUpCHChan,
			WriteToTunnelC:  cm.writeToTunnelC,
			ReadFromTunnelC: rc,
			mutex:           mu,
		}
		cm.poolLock.Lock()
		cm.connHandlerPool[NewCCID(cm.clientID, cid)] = ch
		cm.poolLock.Unlock()
		// TODO send to a work pool for performance reason
		go func() {
			// TODO fast open feature
			if cm.fastOpen {
				log.Debug("CM: Try to create peer connection with fast open")
				ch.CreateFastOpenConn()
			} else {
				log.Debug("CM: Try to create peer connection")
				if !ch.CreatePeerConn() {
					// destroy the unused connection handler
					cm.poolLock.Lock()
					ch.Stop()
					delete(cm.connHandlerPool, NewCCID(cm.clientID, cid))
					cm.poolLock.Unlock()

					return
				}
				log.Debug("CM: Created peer connection")
			}
			go ch.Start()
		}()

		if cid == MaxConnID {
			cid = 0
		} else {
			cid += 1
		}
	}
}

func (cm *ConnManager) cleanUpCHPool() {
	for ccid := range cm.cleanUpCHChan {
		cm.poolLock.Lock()
		ch, ok := cm.connHandlerPool[ccid]
		if ok {
			log.Debugf("CM: Clean up connection handler for connection: %d", ch.ConnID)
			close(ch.ReadFromTunnelC)
			delete(cm.connHandlerPool, ccid)
			for _, v := range cm.connHandlerPool {
				log.Debugf("CM: Alive connection handler with connection id: %d", v.ConnID)
			}
		}
		cm.poolLock.Unlock()
	}
}

// Stop connection manager
func (cm *ConnManager) Stop() {
	cm.mutex.Lock()
	defer cm.mutex.Unlock()
	if cm.stopped {
		return
	}

	cm.stopped = true
	close(cm.quit)
	close(cm.readFromTunnelC)
	close(cm.writeToTunnelC)
	close(cm.newCCIDChannel)
	cm.stopAllConnHandler()
	close(cm.cleanUpCHChan)
}

func (cm *ConnManager) stopAllConnHandler() {
	cm.poolLock.Lock()
	defer cm.poolLock.Unlock()
	for k, v := range cm.connHandlerPool {
		v.Stop()
		delete(cm.connHandlerPool, k)
	}
}

func (cm *ConnManager) dispatch() {
	defer func() {
		if r := recover(); r != nil {
			// TODO error handling
			log.Errorf("CM: Recover from error: %s", r)
			go cm.dispatch()
		}
	}()

	for gf := range cm.readFromTunnelC {
		log.Debugf("CM: Received frame from tunnel: %s", gf)
		cm.poolLock.RLock()
		ch, ok := cm.connHandlerPool[NewCCID(gf.clientID, gf.ConnID)]
		cm.poolLock.RUnlock()
		if ok {
			// TODO avoid hang here
			log.Debugf("CM: Found CH in pool, ClientID: %d, ConnID: %d", ch.ClientID, ch.ConnID)
			// TODO "send on closed channel" panic due to cm.cleanUpCHPool()
			ch.ReadFromTunnelC <- gf
			continue
		}

		// TODO fast open feature
		if cm.fastOpen && !gf.IsControl() && gf.SeqNum == FastOpenInitSeqNum {
			go func(gf *GotaFrame) {
				// TODO connection is creating, delay this frame
				cm.readFromTunnelC <- gf
			}(gf)
		} else {
			// TODO connection pool feature
			log.Errorf("CM: Connection didn't exist, client id: %d, connection id: %d, dropped.", gf.clientID, gf.ConnID)
		}
		//if gf.IsControl() && (gf.SeqNum == TMCloseConnSeq || gf.SeqNum == TMCloseConnForceSeq) {
		//	log.Debugf("CM: Received close connection signal for connection %d, delete it from connection pool", gf.ConnID)
		//	delete(cm.connHandlerPool, NewCCID(gf.clientID, gf.ConnID))
		//}
	}
}

type ConnHandler struct {
	rw io.ReadWriteCloser

	writeStopped bool
	readStopped  bool
	mutex        sync.Locker

	ClientID ClientID
	ConnID   uint32

	cleanUpCHChan chan<- CCID

	WriteToTunnelC  chan *GotaFrame
	ReadFromTunnelC chan *GotaFrame
}

func NewConnHandler(rw io.ReadWriteCloser) *ConnHandler {
	return &ConnHandler{
		rw: rw,
	}
}

// Start forward traffic between local request and remote response
func (ch *ConnHandler) Start() {
	ch.mutex = &sync.Mutex{}
	go ch.readFromTunnel()
	go ch.writeToTunnel()
}

// Stop froward traffic
func (ch *ConnHandler) Stop() {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()
	if ch.readStopped && ch.writeStopped {
		return
	}

	close(ch.ReadFromTunnelC)

	// force close the conn, because when read the conn, the goroutine will hang there.
	ch.rw.Close()
	// when force close the conn, discard the latest read error
	ch.writeStopped = true
}

// Stopped return the connection handler's current status
func (ch *ConnHandler) Stopped() bool {
	return ch.readStopped || ch.writeStopped
}

// CreatePeerConn send request to create a peer connection.
// It will wait for the first response, if the response gota frame is not a control for "TMCreateConnOKSeq",
// it will return false.
func (ch *ConnHandler) CreatePeerConn() bool {
	// send create request
	req := &GotaFrame{
		Control:  true,
		clientID: ch.ClientID,
		SeqNum:   TMCreateConnSeq,
		ConnID:   ch.ConnID,
		Length:   0,
	}
	ch.WriteToTunnelC <- req

	// wait response
	resp := <-ch.ReadFromTunnelC
	if resp.Control && resp.SeqNum == TMCreateConnOKSeq {
		log.Debugf("CH: Create peer connection success, response: %s", resp)
		return true
	} else if resp.Control && resp.SeqNum == TMCreateConnErrorSeq {
		log.Debugf("CH: Create peer connection failed, response: %s", resp)
		return false
	}
	log.Errorf("CH: Wrong response from tunnel for creating a peer connection, frame: %s", resp)
	return false
}

// CreateFastOpenConn send request to create a peer connection, but does not wait the response.
func (ch *ConnHandler) CreateFastOpenConn() {
	// send create request
	req := &GotaFrame{
		Control:  true,
		clientID: ch.ClientID,
		SeqNum:   TMCreateConnSeq,
		ConnID:   ch.ConnID,
		Length:   0,
	}
	ch.WriteToTunnelC <- req
	log.Debug("CH: Create peer connection with fast open request send")
}

func (ch *ConnHandler) readFromTunnel() {
	defer Recover()

	drop := func(c chan *GotaFrame) {
		for gf := range c {
			log.Warnf("CH: Connection %d closed, Gota Frame dropped", gf.ConnID)
		}
	}
	defer func() {
		ch.mutex.Lock()
		defer ch.mutex.Unlock()

		// TODO goroutines leak
		go drop(ch.ReadFromTunnelC)

		if cw, ok := ch.rw.(RWCloseWriter); ok {
			cw.CloseWrite()
		} else {
			ch.rw.Close()
		}
		ch.readStopped = true
		if ch.writeStopped {
			ch.cleanUpCHChan <- NewCCID(ch.ClientID, ch.ConnID)
		}
	}()

	var seq uint32
	seq = 0
	cache := make(map[uint32][]byte)

	log.Debugf("CH: Start to read from tunnel, ClientID: %d, ConnID: %d", ch.ClientID, ch.ConnID)

	for gf := range ch.ReadFromTunnelC {
		log.Debugf("CH: Received frame from CM: %s", gf)
		if gf.IsControl() {
			// TODO control signal handle
			if gf.SeqNum == TMCloseConnSeq {
				log.Debugf("CH: Received close connection signal for ConnID: %d", gf.ConnID)
				return
			} else if gf.SeqNum == TMCloseConnForceSeq {
				log.Warnf("CH: Received force close connection signal for ConnID: %d", gf.ConnID)
				ch.rw.Close()
				return
			}
			continue
		}

		if gf.SeqNum == seq {
			log.Debugf("CH: Received wanted data frame seq from tunnel: %d", seq)

			err := WriteNBytes(ch.rw, gf.Length, gf.Payload)
			if err != nil {
				log.Errorf("CH: Write to connection error: %s", err)
				// TODO when write error, the conneciont may be broken
				ch.sendForceCloseGotaFrame()
				return
			}

			seq += 1
			if len(cache) == 0 {
				continue
			}

			// check cache and send to client
			for {
				if data, ok := cache[seq]; ok {
					err := WriteNBytes(ch.rw, len(data), data)
					if err != nil {
						log.Errorf("CH: Write to connection error: %s", err)
						// TODO when write error, the conneciont may be broken
						ch.sendForceCloseGotaFrame()
						return
					}

					delete(cache, seq)
					seq += 1
				} else {
					break
				}
			}
		} else if gf.SeqNum > seq {
			// cache for disorder frame
			log.Debugf("CH: Received frame seq from tunnel: %d, but want to receive frame seq: %d, cache it",
				gf.SeqNum, seq)
			cache[gf.SeqNum] = gf.Payload
		} else {
			log.Warnf("CH: Received frame seq from tunnel: %d, but the data frame already sent, dropped", gf.SeqNum)
		}
	}
}

func (ch *ConnHandler) writeToTunnel() {
	defer Recover()

	defer func() {
		ch.mutex.Lock()
		defer ch.mutex.Unlock()

		if cw, ok := ch.rw.(RWCloseReader); ok {
			cw.CloseRead()
		} else {
			ch.rw.Close()
		}
		ch.writeStopped = true
		if ch.readStopped {
			ch.cleanUpCHChan <- NewCCID(ch.ClientID, ch.ConnID)
		}
	}()

	// read io.EOF and send CloseWrite signal
	var seq uint32
	seq = 0

	log.Debugf("CH: Start to write to tunnel, ClientID: %d, ConnID: %d", ch.ClientID, ch.ConnID)
	for {
		// TODO when to call cache.Put() ?
		//data := cache.Get().([]byte)

		data := make([]byte, MaxDataLength)
		n, err := ch.rw.Read(data)
		if n > 0 {
			gf := &GotaFrame{
				clientID: ch.ClientID,
				ConnID:   ch.ConnID,
				SeqNum:   seq,
				Length:   n,
				Payload:  data[:n],
			}
			log.Debugf("CH: Received data from conn, %s", gf)
			seq += 1
			ch.WriteToTunnelC <- gf
		} else if n == 0 && err != io.EOF {
			log.Warn("CH: Received empty data from connection")
		}

		if err == io.EOF {
			log.Debugf("CH: Received io.EOF, start to close write connection on peer")
			ch.sendCloseGotaFrame()
			return
		} else if err != nil {
			// TODO when read error, the conneciont may be broken
			ch.sendForceCloseGotaFrame()

			// when call Stop function, the conn may be force closed
			ch.mutex.Lock()
			defer ch.mutex.Unlock()

			// ignore error for force stop
			if !ch.writeStopped {
				log.Errorf("CH: Read from connection error: %+v", err)
			}
			return
		}
	}
}

func (ch *ConnHandler) sendCloseGotaFrame() {
	gf := &GotaFrame{
		clientID: ch.ClientID,
		Control:  true,
		ConnID:   ch.ConnID,
		SeqNum:   TMCloseConnSeq,
		Length:   0,
	}
	ch.WriteToTunnelC <- gf
}

func (ch *ConnHandler) sendForceCloseGotaFrame() {
	gf := &GotaFrame{
		clientID: ch.ClientID,
		Control:  true,
		ConnID:   ch.ConnID,
		SeqNum:   TMCloseConnForceSeq,
		Length:   0,
	}
	ch.WriteToTunnelC <- gf
}

type ConnPool struct {
	count       int
	connChannel chan uint32
	quit        chan struct{}
	fastOpen    bool

	WriteToTunnelChan  chan *GotaFrame
	ReadFromTunnelChan chan *GotaFrame
}

func NewConnPool(n int, fastOpen bool, rc, wc chan *GotaFrame) *ConnPool {
	c := make(chan uint32, n-1)
	quit := make(chan struct{})
	return &ConnPool{
		count:       n,
		connChannel: c,
		quit:        quit,
		fastOpen:    fastOpen,

		WriteToTunnelChan:  wc,
		ReadFromTunnelChan: rc,
	}
}

func (c *ConnPool) Start() {
}

func (c *ConnPool) createConn() {
	for {
		// create a peer connection
		connID := uint32(666)

		// send create request
		req := &GotaFrame{
			Control: true,
			SeqNum:  TMCreateConnSeq,
			ConnID:  connID,
			Length:  0,
		}
		c.WriteToTunnelChan <- req

		select {
		case c.connChannel <- connID:
		case <-c.quit:
			return
		}
	}
}

func (c *ConnPool) createConnWithFastOpen() {
	for {
		// create a peer connection
		connID := uint32(666)

		// send create request
		req := &GotaFrame{
			Control: true,
			SeqNum:  TMCreateConnSeq,
			ConnID:  connID,
			Length:  0,
		}
		c.WriteToTunnelChan <- req

		select {
		case c.connChannel <- connID:
		case <-c.quit:
			return
		}
	}
}

func (c *ConnPool) ReadFromTunnel() {
	for gf := range c.ReadFromTunnelChan {
		if gf.Control && gf.SeqNum == TMCreateConnOKSeq {
			c.connChannel <- gf.ConnID
		} else {
			log.Warnf("CP: Received unaccepted gota frame: %s", gf)
		}
	}
}

func (c *ConnPool) GetConnID() uint32 {
	return <-c.connChannel
}

var cache sync.Pool

const MaxRetryTimes = 3

func init() {
	cache = sync.Pool{
		New: func() interface{} {
			c := make([]byte, MaxDataLength)
			return c
		},
	}
}
