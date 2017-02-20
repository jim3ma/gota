package gota

import (
	"bytes"
	"errors"
	log "github.com/Sirupsen/logrus"
	"io"
	"net"
	//"net/http"
	_ "net/http/pprof"
	"testing"
	"time"
)

func TestNewCCID(t *testing.T) {
	cc := NewCCID(1, 2)
	if cc.ClientID() != 1 {
		t.Errorf("Error client ID: %d", cc.ClientID())
	}

	if cc.ConnID() != 2 {
		t.Errorf("Error conn ID: %d", cc.ConnID())
	}

	cc = NewCCID(0xFFFFFFFF, 0xFFFFFFFE)
	if cc.ClientID() != 0xFFFFFFFF {
		t.Errorf("Error client ID: %d", cc.ClientID())
	}

	if cc.ConnID() != 0xFFFFFFFE {
		t.Errorf("Error conn ID: %d", cc.ConnID())
	}
}

// buffer is just here to wrap bytes.Buffer to an io.ReadWriteCloser.
// Read about embedding to see how this works.
type buffer struct {
	strings [][]byte
	rnum    int
	wnum    int
	wclosed bool
	rclosed bool
}

func (b *buffer) Read(p []byte) (n int, err error) {
	l := len(b.strings)
	if b.rnum < l {
		copy(p, b.strings[b.rnum])
		n = len(b.strings[b.rnum])
		err = nil

		b.rnum++
		return
	}
	n = 0
	err = io.EOF
	return
}

func (b *buffer) Write(p []byte) (n int, err error) {
	l := len(b.strings)
	if b.wnum < l {
		if bytes.Compare(p, b.strings[b.wnum]) == 0 {
			log.Infof("Received test string: \"%s\"", b.strings[b.wnum])
			n = len(b.strings[b.wnum])
			err = nil

			b.wnum++
			return
		} else {
			n = 0
			err = errors.New("Mismatch!")
		}
	}
	n = 0
	err = nil
	return
	//return b.Buffer.Write(p)
}

// Add a Close method to our buffer so that we satisfy io.ReadWriteCloser.
func (b *buffer) Close() error {
	//b.Buffer.Reset()
	b.rnum = 0
	b.wnum = 0
	b.wclosed = true
	b.rclosed = true
	return nil
}

func (b *buffer) CloseWrite() error {
	b.wnum = 0
	b.wclosed = true
	return nil
}

func (b *buffer) CloseRead() error {
	b.rnum = 0
	b.rclosed = true
	return nil
}

func newBuffer() *buffer {
	bufLength := 3
	buf := &buffer{
		rnum: 0,
		wnum: 0,
	}
	buf.strings = make([][]byte, bufLength)
	buf.strings[0] = []byte("One of the most useful data structures in computer science is the hash table.")
	buf.strings[1] = []byte("Many hash table implementations exist with varying properties, but in general they offer fast lookups, adds, and deletes. ")
	buf.strings[2] = []byte("Go provides a built-in map type that implements a hash table.")
	return buf
}

func TestConnHandler_Start(t *testing.T) {
	buf := newBuffer()

	rc := make(chan *GotaFrame)
	wc := make(chan *GotaFrame)

	ch := &ConnHandler{
		ClientID:        0,
		ConnID:          0,
		rw:              buf,
		WriteToTunnelC:  wc,
		ReadFromTunnelC: rc,
	}
	ch.Start()

	// received
	go func() {
		for c := range wc {
			t.Logf("Received GotaFrame: %s", c)
			if c.IsControl() {
				continue
			}
			rc <- c
		}
	}()

	time.Sleep(time.Second * 3)

	// close conn
	gf := &GotaFrame{
		Control:  true,
		ConnID:   0,
		clientID: 0,
		SeqNum:   TMCloseConnSeq,
		Length:   0,
	}
	rc <- gf

	time.Sleep(time.Second * 1)

	if !ch.Stopped() {
		t.Error("conn hanlder did not stop")
		ch.Stop()
	}
	if buf.wclosed != true {
		t.Error("buffer does not be closed!")
	}
}

func TestConnHandler_CreatePeerConn(t *testing.T) {
	rc := make(chan *GotaFrame)
	wc := make(chan *GotaFrame)

	ch := &ConnHandler{
		ClientID:        0,
		ConnID:          0,
		rw:              nil,
		WriteToTunnelC:  wc,
		ReadFromTunnelC: rc,
	}

	result := make(chan bool)

	go func() {
		result <- ch.CreatePeerConn()
	}()

	req := <-wc
	log.Debugf("received request: %s", req)
	if !req.Control || req.SeqNum != TMCreateConnSeq {
		t.Error("Error request for create connection")
	}

	resp := &GotaFrame{
		Control:  true,
		ConnID:   0,
		clientID: 0,
		SeqNum:   TMCreateConnOKSeq,
		Length:   0,
	}
	rc <- resp

	if ok := <-result; ok {
		t.Log("Create peer conn ok")
	} else {
		t.Error("Create peer conn error")
	}
}

func TestConnManager_handleNewConn(t *testing.T) {
	buf := newBuffer()
	cm := NewConnManager()
	newConnChannel := make(chan io.ReadWriteCloser)
	go cm.handleNewConn(newConnChannel)

	// new conn
	newConnChannel <- buf

	time.Sleep(time.Second * 1)
	log.Debugf("%s", cm.connHandlerPool)

	// fake response for create peer connection
	req := <-cm.writeToTunnelC
	log.Debugf("received request: %s", req)
	if !req.Control || req.SeqNum != TMCreateConnSeq {
		t.Error("Error request for create connection")
	}

	resp := &GotaFrame{
		Control:  true,
		ConnID:   0,
		clientID: 0,
		SeqNum:   TMCreateConnOKSeq,
		Length:   0,
	}
	cm.connHandlerPool[NewCCID(0, 0)].ReadFromTunnelC <- resp

	// received
	go func() {
		for c := range cm.WriteToTunnelChannel() {
			t.Logf("Received GotaFrame: %s", c)
			if c.IsControl() {
				continue
			}
			cm.connHandlerPool[NewCCID(0, 0)].ReadFromTunnelC <- c
		}
	}()

	time.Sleep(time.Second * 3)

	// close conn
	gf := &GotaFrame{
		Control:  true,
		ConnID:   0,
		clientID: 0,
		SeqNum:   TMCloseConnSeq,
		Length:   0,
	}

	cm.connHandlerPool[NewCCID(0, 0)].ReadFromTunnelC <- gf

	time.Sleep(time.Second * 1)
	for _, ch := range cm.connHandlerPool {
		if !ch.Stopped() {
			t.Error("conn hanlder did not stop normally")
			ch.Stop()
		}
	}
}

func TestConnManager_handleNewCCID(t *testing.T) {
	addr := "127.0.0.1:32768"
	laddr, _ := net.ResolveTCPAddr("tcp4", addr)

	// create a tcp server for testing
	go helpListenAndServe(laddr, t)

	cm := NewConnManager()
	go cm.dispatch()
	go cm.handleNewCCID(laddr)

	ccid := cm.NewCCIDChannel()
	ccid <- NewCCID(0, 0)

	go func() {
		rc := cm.ReadFromTunnelChannel()
		wc := cm.WriteToTunnelChannel()
		for gf := range wc {
			log.Debugf("forward frame: %s", gf)
			rc <- gf
		}
	}()

	//log.Info(cm.connHandlerPool[NewCCID(0, 0)].Stopped())
	time.Sleep(time.Second * 30)
	cm.Stop()
	//log.Info(cm.connHandlerPool[NewCCID(0, 0)].Stopped())
}

func TestConnManager_ListenAndServe(t *testing.T) {
	cm := NewConnManager()
	addr := "127.0.0.1:32769"
	tcpAddr, _ := net.ResolveTCPAddr("tcp", addr)
	go cm.ListenAndServe(addr)

	time.Sleep(time.Second * 1)

	go helpDialAndServe(tcpAddr, t)

	rc := cm.ReadFromTunnelChannel()
	wc := cm.WriteToTunnelChannel()

	// create a peer connection
	req := <-wc
	log.Debugf("received request: %s", req)
	if !req.Control || req.SeqNum != TMCreateConnSeq {
		t.Error("Error request for create connection")
	}

	resp := &GotaFrame{
		Control:  true,
		ConnID:   0,
		clientID: 0,
		SeqNum:   TMCreateConnOKSeq,
		Length:   0,
	}
	rc <- resp

	go func() {

		for gf := range wc {
			log.Debugf("forward frame: %s", gf)
			rc <- gf
		}
	}()

	time.Sleep(time.Second * 10)
	//cm.Stop()
}

func helpDialAndServe(raddr *net.TCPAddr, t *testing.T) {
	conn, _ := net.DialTCP("tcp", nil, raddr)
	helpServe(conn, t)
}

func helpServe(conn *net.TCPConn, t *testing.T) {
	buf := newBuffer()
	go func() {
		data := make([]byte, 65536)
		for {
			n, err := conn.Read(data)
			if n > 0 {
				buf.Write(data[:n])
			}
			if err == io.EOF {
				break
			}

			if err != nil {
				t.Errorf("Read from conn error: %s", err)
			}
			time.Sleep(time.Second * 1)
		}
	}()
	go func() {
		data := make([]byte, 65536)
		for {
			n, err := buf.Read(data)
			if n > 0 {
				//log.Debugf("try to write buf to connection: %s, %d, %d", data, n, len(data))
				_, err = conn.Write(data[:n])
			}
			if err == io.EOF {
				conn.CloseWrite()
				break
			}

			if err != nil {
				t.Errorf("Write to conn error: %s", err)
			}
			time.Sleep(time.Second * 1)
		}
	}()
}

func helpListenAndServe(laddr *net.TCPAddr, t *testing.T) {
	listener, _ := net.ListenTCP("tcp4", laddr)
	conn, _ := listener.AcceptTCP()
	//c <- conn
	log.Debug("new connection received")
	helpServe(conn, t)
}

func init() {
	log.SetLevel(log.DebugLevel)

	// pprof debug
	//go func() {
	//	log.Println(http.ListenAndServe("localhost:6060", nil))
	//}()
}
