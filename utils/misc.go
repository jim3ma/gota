package utils

import (
	"time"
	"bytes"
	"net"
)

// Statistic contains the traffic status
type Statistic struct {
	SentBytes     int64
	ReceivedBytes int64
	StartSeconds  int64
}

func (s *Statistic) AddSentBytes(c int64) {
	s.SentBytes += c
}

func (s *Statistic) AddReceivedBytes(c int64) {
	s.ReceivedBytes += c
}

// SendSpeed return the bytes sent per second
func (s *Statistic) SendSpeed() int64 {
	t := time.Now().Unix() - s.StartSeconds
	return s.SentBytes/int64(t)
}

// ReceiveSpeed return the bytes received per second
func (s *Statistic) ReceiveSpeed() int64 {
	t := time.Now().Unix() - s.StartSeconds
	return s.ReceivedBytes/int64(t)
}

func ReadNBytes(conn *net.TCPConn, n int) ([]byte, error){
	var buf bytes.Buffer
	for remain := n; remain > 0; {
		data := make([]byte, remain)
		rn, err := conn.Read(data)

		if err != nil {
			return nil, err
		}
		remain = remain - rn
		buf.Write(data[:rn])
	}
	return buf.Bytes(), nil
}

func WriteNBytes(conn *net.TCPConn, n int, d []byte) error{
	for wrote := 0; wrote < n; {
		wn, err := conn.Write(d[:wrote])
		if err != nil {
			return err
		}
		wrote = wrote + wn
	}
	return nil
}
