package gota

import (
	log "github.com/Sirupsen/logrus"
	"io"
	"sync"
)

type TunnelManager struct {
	readPool  chan chan *GotaFrame
	writePool chan chan *GotaFrame
}

func (tm *TunnelManager) start() {

}

func (tm *TunnelManager) dispatch() {

}

func (tm *TunnelManager) readDispatch() {
	for {
		select {
		case c := <-tm.readPool:
			<-c
		}
	}
}

func (tm *TunnelManager) writeDispatch() {
	for {
		select {
		case c := <-tm.writePool:
			<-c
		}
	}

}

type TunnelTransport struct {
	mode int

	ClientID uint32
	quit     chan struct{}

	mu      sync.Locker
	quitted bool

	readPool    chan<- chan *GotaFrame
	readChannel chan *GotaFrame

	writePool    chan<- chan *GotaFrame
	writeChannel chan *GotaFrame

	rw io.ReadWriteCloser
}

// ReadFromTunnel reads from tunnel and send to connection manager
func (t *TunnelTransport) readFromTunnel() {
	defer t.Close()
	defer Recover()
	for {
		header, err := ReadNBytes(t.rw, HeaderLength)
		if err != io.EOF && err != nil {
			// TODO
			log.Error("Received data frame header error")
		}
		if err == io.EOF {
			log.Infof("Received data frame header io.EOF, stop this worker")
			break
		}

		var gf GotaFrame
		err = gf.UnmarshalBinary(header)
		if err != nil && err != HeaderOnly {
			log.Error("Error GotaFrame: %+v", header)
		}
		log.Debugf("Received data frame header from server: %s", gf)

		if gf.IsControl() {
			// TODO
		}

		payload, err := ReadNBytes(t.rw, gf.Length)
		if err != nil {
			// TODO
			return
		}
		gf.Payload = payload
		// register the current worker into the worker queue.
		t.readPool <- t.readChannel
		select {
		case t.readChannel <- &gf:
		case <-t.quit:
			return
		}
	}
}

// WriteToTunnel reads from connection and send to tunnel
func (t *TunnelTransport) writeToTunnel() {
	defer t.Close()
	defer Recover()
	for {
		// register the current worker into the worker queue.
		t.writePool <- t.writeChannel

		select {
		case data := <-t.writeChannel:
			// we have received a write request.
			rawBytes, err := data.MarshalBinary()
			if err != nil && nil != HeaderOnly {
				log.Errorf("Marshal GotaFrame error: %+v", err)
			}
			err = WriteNBytes(t.rw, data.Length+HeaderLength, rawBytes)
			if err != nil && nil != io.EOF {
				// TODO
			}
		case <-t.quit:
			// received a signal to stop
			return
		}
	}
}

func (t *TunnelTransport) ListenAndServe() {

}

func (t *TunnelTransport) ConnectAndServe() {

}

// Start method starts the run loop for the worker, listening for a quit channel in
// case we need to stop it
func (t *TunnelTransport) Start() {
	go t.readFromTunnel()
	go t.writeToTunnel()
}

// Stop signals the worker to stop listening for work requests.
func (t *TunnelTransport) Stop() {
	// close a channel will trigger all reading from the channel to return immediately
	close(t.quit)
}

func (t *TunnelTransport) IsClosed() bool {
	return t.quitted
}

func (t *TunnelTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	defer Recover()

	if !t.quitted {
		t.quitted = true
		close(t.readChannel)
		close(t.writeChannel)
		return t.rw.Close()
	}
	return nil
}
