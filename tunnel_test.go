package gota

import (
	"bytes"
	"io"
	"testing"

	log "github.com/Sirupsen/logrus"
	"net"
	"time"
)

func TestTunnelManager_SetConfig(t *testing.T) {
	tm := NewTunnelManager(nil, nil)
	ac := TunnelActiveConfig{}
	if err := tm.SetConfig(ac); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}

	if tm.Mode() != ActiveMode {
		t.Error("Tunnel manager work mode error")
	}

	acs := make([]TunnelActiveConfig, 10)
	if err := tm.SetConfig(acs); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}
	if tm.Mode() != ActiveMode {
		t.Error("Tunnel manager work mode error")
	}

	pc := TunnelPassiveConfig{}
	if err := tm.SetConfig(pc); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}
	if tm.Mode() != PassiveMode {
		t.Error("Tunnel manager work mode error")
	}

	pcs := make([]TunnelPassiveConfig, 10)
	if err := tm.SetConfig(pcs); err != nil {
		t.Errorf("Set config for tunnel manager error: %s", err)
	}
	if tm.Mode() != PassiveMode {
		t.Error("Tunnel manager work mode error")
	}

	if err := tm.SetConfig(1); err == nil {
		t.Errorf("Set config for tunnel manager error")
	}
}

// buffer is just here to make bytes.Buffer an io.ReadWriteCloser.
// Read about embedding to see how this works.
type bufferWithClose struct {
	bytes.Buffer
}

// Add a Close method to our buffer so that we satisfy io.ReadWriteCloser.
func (b *bufferWithClose) Close() error {
	b.Buffer.Reset()
	return nil
}

type bidirectionBufferEnd struct {
	rc  chan []byte
	wc  chan []byte
	buf bytes.Buffer
}

func (b *bidirectionBufferEnd) Read(p []byte) (n int, err error) {
	if b.buf.Len() <= 0 {
		buf, ok := <-b.rc
		if ok {
			b.buf.Write(buf)
		}
	}
	n, err = b.buf.Read(p)
	return
}

func (b *bidirectionBufferEnd) Write(p []byte) (n int, err error) {
	n = len(p)
	b.wc <- p
	err = nil
	return
}

// Add a Close method to our buffer so that we satisfy io.ReadWriteCloser.
func (b *bidirectionBufferEnd) Close() error {
	//close(b.wc)
	return nil
}

func (b *bidirectionBufferEnd) CloseWrite() error {
	//close(b.wc)
	return nil
}

func (b *bidirectionBufferEnd) CloseRead() error {
	return nil
}

type bidirectBuffer struct {
	c1 chan []byte
	c2 chan []byte
}

func newBidirectBuffer() *bidirectBuffer {
	c1 := make(chan []byte)
	c2 := make(chan []byte)
	return &bidirectBuffer{
		c1: c1,
		c2: c2,
	}
}

func (b *bidirectBuffer) Ends() (io.ReadWriteCloser, io.ReadWriteCloser) {
	return &bidirectionBufferEnd{
			rc: b.c1,
			wc: b.c2,
		}, &bidirectionBufferEnd{
			wc: b.c1,
			rc: b.c2,
		}
}

func Test_bidirectBuffer(t *testing.T) {
	buf := newBidirectBuffer()
	e1, e2 := buf.Ends()

	go e1.Write([]byte("e1 write"))
	data := make([]byte, 1024)
	n, err := e2.Read(data)
	if err != nil {
		log.Error("")
	}
	log.Infof("read from e1: %d", n)

	go e2.Write([]byte("e2 write"))
	data2 := make([]byte, 1024)
	n, err = e1.Read(data2)
	if err != nil {
		log.Error("")
	}
	log.Infof("read from e2: %d", n)
}

func TestTunnelTransport_Start(t *testing.T) {
	buf := newBidirectBuffer()
	e1, e2 := buf.Ends()

	rp1 := make(chan chan *GotaFrame)
	wp1 := make(chan chan *GotaFrame)
	t1 := NewTunnelTransport(wp1, rp1, e1)
	t1.Start()

	rp2 := make(chan chan *GotaFrame)
	wp2 := make(chan chan *GotaFrame)
	t2 := NewTunnelTransport(wp2, rp2, e2)
	t2.Start()

	//go t1.writeToPeerTunnel()
	//go t2.readFromPeerTunnel()

	buf2 := newBuffer()
	for {
		data := make([]byte, 65536)
		n, err := buf2.Read(data)
		if err == io.EOF {
			break
		}

		gf := &GotaFrame{
			clientID: 0,
			Length:   n,
			ConnID:   0,
			Payload:  data[:n],
		}
		<-rp1 <- gf
		gf2 := <-<-wp2
		if gf2.Length != n && bytes.Compare(gf.Payload, gf2.Payload) != 0 {
			t.Errorf("Received error gota frame: %s, except: %s", gf2, gf)
		}
	}

	//go t1.readFromPeerTunnel()
	//go t2.writeToPeerTunnel()

	buf3 := newBuffer()
	for {
		data := make([]byte, 65536)
		n, err := buf3.Read(data)
		if err == io.EOF {
			break
		}

		gf := &GotaFrame{
			clientID: 0,
			Length:   n,
			ConnID:   0,
			Payload:  data[:n],
		}
		<-rp2 <- gf
		gf2 := <-<-wp1
		if gf2.Length != n && bytes.Compare(gf.Payload, gf2.Payload) != 0 {
			t.Errorf("Received error gota frame: %s, except: %s", gf2, gf)
		}
	}

	time.Sleep(time.Second * 5)
	t1.Stop()
	//t2.Stop()
}

func TestTunnelManager_Start(t *testing.T) {
	addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:32770")

	rc1 := make(chan *GotaFrame)
	wc1 := make(chan *GotaFrame)
	tm1 := NewTunnelManager(rc1, wc1)
	pc := TunnelPassiveConfig{
		TCPAddr: addr,
	}
	tm1.SetConfig(pc)
	tm1.Start()

	rc2 := make(chan *GotaFrame)
	wc2 := make(chan *GotaFrame)
	tm2 := NewTunnelManager(rc2, wc2)

	ac := TunnelActiveConfig{
		LocalAddr:  nil,
		RemoteAddr: addr,
	}
	tm2.SetConfig(ac)
	tm2.Start()

	payload := []byte("payload")

	gf1 := &GotaFrame{
		Control: false,
		ConnID:  0,
		SeqNum:  0,
		Length:  len(payload),
		Payload: payload,
	}

	rc1 <- gf1
	gf2 := <-wc2

	if !CompareGotaFrame(gf1, gf2) {
		t.Errorf("Error Gota Frame received, wanted %s, but received %s", gf1, gf2)
	}

	gf3 := &GotaFrame{
		Control: false,
		ConnID:  1,
		SeqNum:  0,
		Length:  len(payload),
		Payload: payload,
	}

	rc2 <- gf3
	gf4 := <-wc1

	if !CompareGotaFrame(gf3, gf4) {
		t.Errorf("Error Gota Frame received, wanted %s, but received %s", gf3, gf4)
	}
}
