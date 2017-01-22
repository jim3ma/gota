package utils

import "time"

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

