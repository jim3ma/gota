package gota

import (
	"bytes"
	"io"
	"time"
)

// Statistic contains the traffic status
type Statistic struct {
	SentBytes     uint64
	ReceivedBytes uint64
	StartSeconds  int64
}

// AddSentBytes update the SentBytes by bytes
func (s *Statistic) AddSentBytes(c uint64) {
	s.SentBytes += c
}

// AddReceivedBytes update the ReceivedBytes by bytes
func (s *Statistic) AddReceivedBytes(c uint64) {
	s.ReceivedBytes += c
}

// SendSpeed return the bytes sent per second
func (s *Statistic) SendSpeed() uint64 {
	t := time.Now().Unix() - s.StartSeconds
	return s.SentBytes / uint64(t)
}

// ReceiveSpeed return the bytes received per second
func (s *Statistic) ReceiveSpeed() uint64 {
	t := time.Now().Unix() - s.StartSeconds
	return s.ReceivedBytes / uint64(t)
}

func ReadNBytes(r io.Reader, n int) ([]byte, error) {
	var buf bytes.Buffer
	for remain := n; remain > 0; {
		data := make([]byte, remain)
		rn, err := r.Read(data)

		if err != nil {
			return nil, err
		}
		remain = remain - rn
		buf.Write(data[:rn])
	}
	return buf.Bytes(), nil
}

func WriteNBytes(w io.Writer, n int, d []byte) error {
	for wrote := 0; wrote < n; {
		wn, err := w.Write(d[wrote:])
		if err != nil {
			return err
		}
		wrote = wrote + wn
	}
	return nil
}

type RWCloseWriter interface {
	CloseWrite() error
	io.ReadWriteCloser
}

type RWCloseReader interface {
	CloseRead() error
	io.ReadWriteCloser
}
