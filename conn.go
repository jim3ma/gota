package gota

import (
	log "github.com/Sirupsen/logrus"
	"io"
	"math"
	"net"
	"sync"
	"time"
)

// CCID combines client ID and connection ID into a uint64 for the key of map struct
// ┏━━━━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━━┓
// ┃   client id: 32 bit   ┃ connection id: 32 bit ┃
// ┗━━━━━━━━━━━━━━━━━━━━━━━┻━━━━━━━━━━━━━━━━━━━━━━━┛
type CCID uint64

// NewCCID return a new CCID with client id and conn id
func NewCCID(clientID uint32, connID uint32) (cc CCID) {
	c := uint64(clientID)
	cc = CCID(c<<32 + uint64(connID))
	return
}

// clientID return client id
func (cc CCID) ClientID() uint32 {
	return uint32(cc >> 32)
}

// ConnID return connection id
func (cc CCID) ConnID() uint32 {
	return uint32(cc & 0x00000000FFFFFFFF)
}

// ConnManager manage connections from listening from local port or connecting to remote server
type ConnManager struct {
	//clientID uint32
	//mode int
	//newConnChannel  chan io.ReadWriteCloser

	newCCIDChannel  chan CCID
	connHandlerPool map[CCID]*ConnHandler

	writeToTunnelC  chan *GotaFrame
	readFromTunnelC chan *GotaFrame

	quit    chan struct{}
	stopped bool
	mutex   sync.Locker
}

// NewConnManager returns a new ConnManager,
// We should use the channels of this ConnManager to set up TunnelMangager
func NewConnManager() *ConnManager {
	ncc := make(chan CCID)
	chPool := make(map[CCID](*ConnHandler))

	wc := make(chan *GotaFrame)
	rc := make(chan *GotaFrame)

	q := make(chan struct{})
	l := &sync.Mutex{}
	return &ConnManager{
		//clientID: 0,
		//mode: 0,
		//newConnChannel: nc,

		newCCIDChannel:  ncc,
		connHandlerPool: chPool,

		writeToTunnelC:  wc,
		readFromTunnelC: rc,

		quit:  q,
		mutex: l,
	}
}

// NewCCIDChannel returns the newCCIDChannel for creating a new connection
func (cm *ConnManager) NewCCIDChannel() chan CCID {
	return cm.newCCIDChannel
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
		panic(err)
	}

	go cm.dispatch()

	newConnChannel := make(chan io.ReadWriteCloser)
	go cm.handleNewConn(newConnChannel)

	go func() {
		// TODO graceful shutdown?
		select {
		case <-cm.quit:
			log.Info("CM: Received quit signal")
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
	return nil
}

// Serve just waits request for new connections from tunnel
// This function can be used in both client and server
func (cm *ConnManager) Serve(addr string) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}
	go cm.dispatch()
	cm.handleNewCCID(tcpAddr)
}

func (cm *ConnManager) handleNewCCID(addr *net.TCPAddr) {
	for cc := range cm.newCCIDChannel {
		log.Debugf("CM: New CCID comes from tunnel, CCID: %d", cc)
		go cm.dialAndCreateCH(cc, addr)
	}
}

func (cm *ConnManager) dialAndCreateCH(cc CCID, addr *net.TCPAddr) {
	var conn io.ReadWriteCloser
	var err error
	retry := 0
	for {
		conn, err = net.DialTCP("tcp", nil, addr)
		if err == nil {
			break
		} else {
			retry += 1
			sec := int32(math.Exp2(float64(retry)))
			log.Debugf("CM: Create a new connection to remote error: %s, retry %d times after %d seconds",
				err, retry, sec)
			time.Sleep(time.Second * time.Duration(sec))
		}

		if retry >= MaxRetryTimes {
			log.Debugf("CM: Create a new connection to remote error after retry %d times", retry)

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
	rc := make(chan *GotaFrame)
	ch := &ConnHandler{
		ClientID:        cc.ClientID(),
		ConnID:          cc.ConnID(),
		rw:              conn,
		WriteToTunnelC:  cm.writeToTunnelC,
		ReadFromTunnelC: rc,
	}
	cm.connHandlerPool[cc] = ch
	go ch.Start()

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

func (cm *ConnManager) handleNewConn(newChannel chan io.ReadWriteCloser) {
	var cid uint32 = 0
	for c := range newChannel {
		// create and start a new ConnHandler with a new connection id, than append to cm.connHandlerPool
		log.Debugf("CM: new connection, id: %d", cid)
		rc := make(chan *GotaFrame)
		ch := &ConnHandler{
			ClientID:        0,
			ConnID:          cid,
			rw:              c,
			WriteToTunnelC:  cm.writeToTunnelC,
			ReadFromTunnelC: rc,
		}
		cm.connHandlerPool[NewCCID(0, cid)] = ch
		// TODO send to a work pool for performance reason
		go func() {
			log.Debug("CM: Try to create peer connection")
			if !ch.CreatePeerConn() {
				log.Error("CM: Create peer connection error")
				// destroy the unused connection handler
				return
			}
			log.Debug("CM: Created peer connection")
			go ch.Start()
		}()

		if cid == MaxConnID {
			cid = 0
		} else {
			cid += 1
		}
	}
}

func (cm *ConnManager) cleanCHPool() {
	for {
		select {
		case <-cm.quit:
			return
		case <-time.After(time.Second * 60):
			for k, v := range cm.connHandlerPool {
				if v.Stopped() {
					delete(cm.connHandlerPool, k)
				}
			}
		}
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
}

func (cm *ConnManager) stopAllConnHandler() {
	for k, v := range cm.connHandlerPool {
		v.Stop()
		delete(cm.connHandlerPool, k)
	}
}

func (cm *ConnManager) dispatch() {
	for gf := range cm.readFromTunnelC {
		log.Debugf("CM: Received frame from tunnel: %s", gf)
		if ch, ok := cm.connHandlerPool[NewCCID(gf.clientID, gf.ConnID)]; ok {
			// TODO use work pool to avoid hang here
			ch.ReadFromTunnelC <- gf
		} else {
			log.Errorf("CM: Connection didn't exist, connection id: %d, dropped.", gf.ConnID)
		}
	}
}

type ConnHandler struct {
	rw io.ReadWriteCloser

	writeStopped bool
	readStopped  bool
	mutex        sync.Locker

	ClientID uint32
	ConnID   uint32

	WriteToTunnelC  chan *GotaFrame
	ReadFromTunnelC chan *GotaFrame
}

func NewConnHandler(rw io.ReadWriteCloser) *ConnHandler {
	return &ConnHandler{
		rw: rw,
	}
}

// Start forward traffic between local request and remote reponse
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

// Stopped return the connection hanlder's current status
func (ch *ConnHandler) Stopped() bool {
	return ch.readStopped || ch.writeStopped
}

// CreatePeerConn send request to create a peer connection.
// It will wait for the first response, if the reponse goto frame is not a control for "TMCreateConnOKSeq",
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
	}
	log.Errorf("CH: Wrong response from tunnel for creating a peer connection, frame: %s", resp)
	return false
}

func (ch *ConnHandler) readFromTunnel() {
	defer func() {
		if cw, ok := ch.rw.(RWCloseWriter); ok {
			cw.CloseWrite()
		} else {
			ch.rw.Close()
		}
		ch.readStopped = true
	}()

	defer Recover()

	var seq uint32
	seq = 0
	cache := make(map[uint32][]byte)

	log.Debugf("CH: Start to read from tunnel, conn id: %d", ch.ClientID)
	for gf := range ch.ReadFromTunnelC {
		if gf.IsControl() {
			// TODO control signal handle
			if gf.SeqNum == TMCloseConnSeq {
				log.Debug("CH: Received close connection signal")
				return
			} else if gf.SeqNum == TMCloseConnForceSeq {
				log.Debug("CH: Received force close connection signal")
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
	defer func() {
		if cw, ok := ch.rw.(RWCloseReader); ok {
			cw.CloseRead()
		} else {
			ch.rw.Close()
		}
		ch.writeStopped = true
	}()

	defer Recover()
	// read io.EOF and send CloseWrite signal
	var seq uint32
	seq = 0

	log.Debugf("CH: Start to write to tunnel, conn id: %d", ch.ClientID)
	for {
		// TODO when to call cache.Put() ?
		//data := cache.Get().([]byte)

		data := make([]byte, MaxDataLength)
		n, err := ch.rw.Read(data)
		if n > 0 {
			gf := &GotaFrame{
				ConnID:  ch.ConnID,
				SeqNum:  seq,
				Length:  n,
				Payload: data[:n],
			}
			log.Debugf("CH: Received data from conn, %s", gf)
			seq += 1
			ch.WriteToTunnelC <- gf
		} else if err != io.EOF {
			log.Warn("CH: Received empty data from connection")
		}

		if err == io.EOF {
			log.Debugf("CH: Received io.EOF, start to close write connection on server")
			ch.sendCloseGotaFrame()
			return
		} else if err != nil {
			// TODO when read error, the conneciont may be broken
			ch.sendForceCloseGotaFrame()

			// when call Stop function, the conn may be force closed
			ch.mutex.Lock()
			defer ch.mutex.Unlock()

			if ch.writeStopped {
				return
			} else {
				log.Errorf("CH: Read from connection error %+v", err)
			}
			return
		}
	}
}

func (ch *ConnHandler) sendCloseGotaFrame() {
	gf := &GotaFrame{
		Control: true,
		ConnID:  ch.ConnID,
		SeqNum:  TMCloseConnSeq,
		Length:  0,
	}
	ch.WriteToTunnelC <- gf
}

func (ch *ConnHandler) sendForceCloseGotaFrame() {
	gf := &GotaFrame{
		Control: true,
		ConnID:  ch.ConnID,
		SeqNum:  TMCloseConnForceSeq,
		Length:  0,
	}
	ch.WriteToTunnelC <- gf
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
