package gota

import (
	"io"
	"net"
	log "github.com/Sirupsen/logrus"
)

// ConnManager manage connections from listening from local port or connecting to remote server
type ConnManager struct {
	//clientID uint32
	//mode int
	newConnChannel  chan io.ReadWriteCloser
	newCCIDChannel  chan CCID
	connHandlerPool map[CCID]*ConnHandler

	WriteToTunnelC  chan *GotaFrame
	ReadFromTunnelC chan *GotaFrame

	quit chan struct{}
}

// CCID combines client ID and connection ID
type CCID uint64

func NewCCID(clientID uint32, connID uint32) (cc CCID) {
	c := uint64(clientID)
	c1 := c << 32 + uint64(connID)
	cc = CCID(c1)
	return
}

func (cc CCID) GetClientID() uint32 {
	return uint32(cc >> 32)
}

func (cc CCID) GetConnID() uint32 {
	return uint32(cc & 0x00000000FFFFFFFF)
}

func NewConnManager()*ConnManager{
	nc := make(chan io.ReadWriteCloser)
	ncc := make(chan CCID)
	chPool := make(map[CCID](*ConnHandler))

	wc := make(chan *GotaFrame)
	rc := make(chan *GotaFrame)

	q := make(chan struct{})
	return &ConnManager{
		//clientID: 0,
		//mode: 0,
		newConnChannel: nc,
		newCCIDChannel: ncc,
		connHandlerPool: chPool,

		WriteToTunnelC: wc,
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

	go cm.handleNewConn()
	go cm.dispatch()

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Errorf("CM: Accept Connection Error: %s", err)
			panic(err)
		}
		log.Debugf("CM: Received new connection from %s", conn.RemoteAddr())
		cm.newConnChannel <- conn

		// TODO graceful shutdown
		select {
		case <- cm.quit:
			log.Info("CM: Received quit signal")
			close(cm.newConnChannel)
			close(cm.ReadFromTunnelC)
			listener.Close()
			return nil
		default:

		}
	}
}

// Serve just waits connection from tunnel
// This function can be both client and server
func (cm *ConnManager) Serve(addr string){
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}

	for cc := range cm.newCCIDChannel {
		log.Debugf("CM: New CCID comes from tunnel, CCID: %d", cc)
		go func(cc CCID) {
			conn, err := net.DialTCP("tcp", nil, tcpAddr)
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
			ch.Start()
		}(cc)
	}

}


func (cm *ConnManager) handleNewConn(){
	var cid uint32 = 0
	for c := range cm.newConnChannel {
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
	}
}

func (cm *ConnManager) Stop(){
	close(cm.quit)
}

func (cm *ConnManager) dispatch(){
	for gf := range cm.ReadFromTunnelC {
		if ch, ok := cm.connHandlerPool[NewCCID(gf.clientID, gf.ConnId)]; ok {
			ch.ReadFromTunnelC <- gf
		} else {
			log.Errorf("CM: Connection didn't exist, connection id: %d, dropped.", gf.ConnId)
		}
	}
}

type ConnHandler struct {
	rw io.ReadWriteCloser

	ClientID uint32
	ConnID   uint32

	WriteToTunnelC  chan *GotaFrame
	ReadFromTunnelC chan *GotaFrame
}

func NewConnHandler(rw io.ReadWriteCloser) *ConnHandler{
	return &ConnHandler{
		rw: rw,
	}
}

func (ch *ConnHandler) Start(){

}

func (ch *ConnHandler) Stop(){

}

func (ch *ConnHandler) readFromTunnel(){
	// rw.CloseWrite() and ch.Stop
	if cw, ok := ch.rw.(RWCloseWriter); ok {
		cw.CloseWrite()
	} else {
		ch.rw.Close()
	}

}

func (ch *ConnHandler) writeToTunnel(){
	// read io.EOF and send CloseWrite signal

}