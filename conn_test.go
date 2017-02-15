package gota

import (
	"testing"
	"bytes"
	"io"
	"errors"
	"time"
	log "github.com/Sirupsen/logrus"
)

func TestConnManager_ListenAndServe(t *testing.T) {

}

func TestNewCCID(t *testing.T) {
	cc := NewCCID(1, 2)
	if cc.GetClientID() != 1 {
		t.Errorf("Error client ID: %d", cc.GetClientID())
	}

	if cc.GetConnID() != 2 {
		t.Errorf("Error conn ID: %d", cc.GetConnID())
	}

	cc = NewCCID(0xFFFFFFFF, 0xFFFFFFFE)
	if cc.GetClientID() != 0xFFFFFFFF {
		t.Errorf("Error client ID: %d", cc.GetClientID())
	}

	if cc.GetConnID() != 0xFFFFFFFE {
		t.Errorf("Error conn ID: %d", cc.GetConnID())
	}
}

// buffer is just here to wrap bytes.Buffer to an io.ReadWriteCloser.
// Read about embedding to see how this works.
type buffer struct {
	//bytes.Buffer
	strings [][]byte
	rnum int
	wnum int
}

func (b *buffer) Read(p []byte) (n int, err error) {
	l := len(b.strings)
	if b.rnum < l {
		copy(p, b.strings[b.rnum])
		n = len(b.strings[b.rnum])
		err = nil

		b.rnum ++
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

			b.wnum ++
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
	return nil
}

func TestConnHandler_Start(t *testing.T) {
	log.SetLevel(log.DebugLevel)

	bufLength := 3
	buf := &buffer{
		rnum: 0,
		wnum: 0,
	}
	buf.strings = make([][]byte, bufLength)
	buf.strings[0] = []byte("One of the most useful data structures in computer science is the hash table.")
	buf.strings[1] = []byte("Many hash table implementations exist with varying properties, but in general they offer fast lookups, adds, and deletes. ")
	buf.strings[2] = []byte("Go provides a built-in map type that implements a hash table.")

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
	} ()

	time.Sleep(time.Second * 3)
	if ! ch.Stopped() {
		ch.Stop()
	}
}