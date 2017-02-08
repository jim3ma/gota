package gota

import (
	"io"
	"net"
	log "github.com/Sirupsen/logrus"
)

// ConnManager manage connections from listening from local port or connecting to remote server
type ConnManager struct {
	mode int
	newConnChannel chan *net.TCPConn
	chPool   map[uint32]*ConnHandler
}

func NewConnManager(){

}

func (cm *ConnManager) ListenAndServe(addr string) error {
	tcpAddr, err := net.ResolveTCPAddr("tcp", addr)
	if err != nil {
		panic(err)
	}

	listener, err := net.ListenTCP("tcp", tcpAddr)
	if err != nil {
		panic(err)
	}

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			log.Errorf("Accept Connection Error: %s", err)
			panic(err)
		}
		log.Debugf("Received new connection from %s", conn.RemoteAddr())
		cm.newConnChannel <- conn
	}
}


func (cm *ConnManager) handleNewConn(){
	var cid uint32 = 1
	for c := range cm.newConnChannel {
		// create and start a new ConnHandler with a new connection id, than append to cm.chPool
		if cid == MaxConnID {
			cid = 1
		} else {
			cid += 1
		}
		log.Debugf("%+v", c)
	}
}

func (cm *ConnManager) Serve(){

}

func (cm *ConnManager) Start(){

}

func (cm *ConnManager) Stop(){

}

func (cm *ConnManager) dispatch(){

}

type ConnHandler struct {
	rw io.ReadWriteCloser
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