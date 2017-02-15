package gota

import (
	log "github.com/Sirupsen/logrus"
	"io"
	"net"
	"sync"
)

// CCID combines client ID and connection ID into a uint64 for the key of map struct
type CCID uint64

func NewCCID(clientID uint32, connID uint32) (cc CCID) {
	c := uint64(clientID)
	cc = CCID(c<<32 + uint64(connID))
	return
}

func (cc CCID) GetClientID() uint32 {
	return uint32(cc >> 32)
}

func (cc CCID) GetConnID() uint32 {
	return uint32(cc & 0x00000000FFFFFFFF)
}

// ConnManager manage connections from listening from local port or connecting to remote server
type ConnManager struct {
	//clientID uint32
	//mode int
	//newConnChannel  chan io.ReadWriteCloser
	newCCIDChannel  chan CCID
	connHandlerPool map[CCID]*ConnHandler

	WriteToTunnelC  chan *GotaFrame
	ReadFromTunnelC chan *GotaFrame

	quit chan struct{}
	stopped bool
}

func NewConnManager() *ConnManager {
	ncc := make(chan CCID)
	chPool := make(map[CCID](*ConnHandler))

	wc := make(chan *GotaFrame)
	rc := make(chan *GotaFrame)

	q := make(chan struct{})
	return &ConnManager{
		//clientID: 0,
		//mode: 0,
		//newConnChannel: nc,
		newCCIDChannel:  ncc,
		connHandlerPool: chPool,

		WriteToTunnelC:  wc,
		ReadFromTunnelC: rc,

		quit: q,
	}
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

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Errorf("CM: Accept Connection Error: %s", err)
			panic(err)
		}
		log.Debugf("CM: Received new connection from %s", conn.RemoteAddr())
		newConnChannel <- conn

		// TODO graceful shutdown
		select {
		case <-cm.quit:
			log.Info("CM: Received quit signal")
			close(newConnChannel)
			listener.Close()
			return nil
		default:

		}
	}
}

// Serve just waits connection from tunnel
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
		go func(cc CCID) {
			conn, err := net.DialTCP("tcp", nil, addr)
			if err != nil {
				log.Debugf("CM: Create new connection to remote error: %s", err)
				return
			}
			rc := make(chan *GotaFrame)
			ch := &ConnHandler{
				ClientID:        cc.GetClientID(),
				ConnID:          cc.GetConnID(),
				rw:              conn,
				WriteToTunnelC:  cm.WriteToTunnelC,
				ReadFromTunnelC: rc,
			}
			cm.connHandlerPool[cc] = ch
			go ch.Start()
		}(cc)
	}
}

func (cm *ConnManager) handleNewConn(newC chan io.ReadWriteCloser) {
	var cid uint32 = 0
	for c := range newC {
		// create and start a new ConnHandler with a new connection id, than append to cm.connHandlerPool
		if cid == MaxConnID {
			cid = 1
		} else {
			cid += 1
		}
		rc := make(chan *GotaFrame)
		ch := &ConnHandler{
			ClientID:        0,
			ConnID:          cid,
			rw:              c,
			WriteToTunnelC:  cm.WriteToTunnelC,
			ReadFromTunnelC: rc,
		}
		cm.connHandlerPool[NewCCID(0, cid)] = ch
		go ch.Start()
	}
}

func (cm *ConnManager) Stop() {
	close(cm.quit)
	close(cm.ReadFromTunnelC)
	close(cm.WriteToTunnelC)
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
	for gf := range cm.ReadFromTunnelC {
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

	stopped bool
	mutex   sync.Locker

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

func (ch *ConnHandler) Start() {
	ch.mutex = &sync.Mutex{}
	go ch.readFromTunnel()
	go ch.writeToTunnel()
}

func (ch *ConnHandler) Stop() {
	ch.mutex.Lock()
	defer ch.mutex.Unlock()
	if ch.stopped {
		return
	}
	ch.stopped = true
	close(ch.ReadFromTunnelC)
	ch.rw.Close()
}

func (ch *ConnHandler) Stopped() bool {
	return ch.stopped
}

func (ch *ConnHandler) readFromTunnel() {
	defer Recover()

	var seq uint32
	seq = 0
	cache := make(map[uint32][]byte)

	for gf := range ch.ReadFromTunnelC {
		if gf.IsControl() && gf.SeqNum == TMCloseConnSeq {
			if cw, ok := ch.rw.(RWCloseWriter); ok {
				cw.CloseWrite()
			} else {
				ch.rw.Close()
			}
			return
		}

		if gf.SeqNum == seq {
			log.Debugf("CH: Received wanted data frame seq from tunnel: %d", seq)

			err := WriteNBytes(ch.rw, gf.Length, gf.Payload)
			if err != nil {
				log.Errorf("CH: Write to connection error: %s", err)
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
	// read io.EOF and send CloseWrite signal
	var seq uint32
	seq = 0
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
			seq += 1
			ch.WriteToTunnelC <- gf
		} else if err != io.EOF {
			log.Warn("CH: Received empty data from connection")
		}

		if err == io.EOF {
			log.Debugf("CH: Received io.EOF, start to close write connection on server")
			ch.sendCloseGotaFrame()

			// TODO need to close ?
			if cw, ok := ch.rw.(RWCloseReader); ok {
				cw.CloseRead()
			} else {
				ch.rw.Close()
			}
			return
		} else if err != nil {
			ch.sendCloseGotaFrame()

			// TODO ?
			ch.mutex.Lock()
			defer ch.mutex.Unlock()

			if ch.stopped {
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
		ConnID: ch.ConnID,
		SeqNum: TMCloseConnSeq,
		Length: 0,
	}
	ch.WriteToTunnelC <- gf
}

var cache sync.Pool

func init() {
	cache = sync.Pool{
		New: func() interface{} {
			c := make([]byte, MaxDataLength)
			return c
		},
	}
}
