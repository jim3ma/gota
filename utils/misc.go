package utils

import "time"

// Statistic contains the traffic status
type Statistic struct {
	SentBytes     uint64
	ReceivedBytes uint64
	StartSeconds  int64
}

func (s *Statistic) AddSentBytes(c uint64) {
	s.SentBytes += c
}

func (s *Statistic) AddReceivedBytes(c uint64) {
	s.ReceivedBytes += c
}

// SendSpeed return the bytes sent per second
func (s *Statistic) SendSpeed() uint64 {
	t := time.Now().Unix() - s.StartSeconds
	return s.SentBytes/uint64(t)
}

// ReceiveSpeed return the bytes received per second
func (s *Statistic) ReceiveSpeed() uint64 {
	t := time.Now().Unix() - s.StartSeconds
	return s.ReceivedBytes/uint64(t)
}

