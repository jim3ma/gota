package gota

import (
	"bytes"
	"errors"
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

var ErrNoMoreBytes = errors.New("Read io.EOF, received bytes count less than required")

// ReadNBytes read N bytes from io.Reader,
// it never returns the io.EOF.
//
// If it read N bytes from io.Reader, returns nil.
// If it read io.EOF, but less than N bytes, return ErrNoMoreBytes.
// If it read other errors, returns them.
func ReadNBytes(r io.Reader, n int) ([]byte, error) {
	var buf bytes.Buffer
	remain := n
	for remain > 0 {
		data := make([]byte, remain)
		rn, err := r.Read(data)

		if err != nil && err != io.EOF {
			return nil, err
		}

		remain = remain - rn
		buf.Write(data[:rn])

		if err == io.EOF {
			break
		}
	}

	if remain > 0 {
		return buf.Bytes(), ErrNoMoreBytes
	}
	return buf.Bytes(), nil
}

// WriteNBytes write N bytes to io.Writer
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

// RWCloseWriter add CloseWrite to io.ReadWriteCloser for only closing write
// after call func CloseWrite, io.ReadWriteCloser can still be read
type RWCloseWriter interface {
	CloseWrite() error
	io.ReadWriteCloser
}

// RWCloseReader add CloseRead to io.ReadWriteCloser for only closing read
// after call func CloseRead, io.ReadWriteCloser can still be write
type RWCloseReader interface {
	CloseRead() error
	io.ReadWriteCloser
}
