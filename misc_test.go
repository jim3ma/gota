package utils

import (
	"testing"
	"bytes"
	"io"
)

func TestStatistic_AddSentBytes(t *testing.T) {
	s := &Statistic{}
	sent := uint64(1024)
	s.AddSentBytes(sent)
	if s.SentBytes != 1024 {
		t.Errorf("Error SentBytes: %d", s.SentBytes)
	}
}

func TestStatistic_AddReceivedBytes(t *testing.T) {
	s := &Statistic{}
	sent := uint64(1024)
	s.AddReceivedBytes(sent)
	if s.ReceivedBytes != 1024 {
		t.Errorf("Error ReceivedBytes: %d", s.ReceivedBytes)
	}
}

func TestReadNBytes(t *testing.T) {
	str := "Hello world, I'm Jim"
	lens := 20
	var testBuf bytes.Buffer
	testBuf.Write([]byte(str))

	full, err := ReadNBytes(&testBuf, lens)
	if err != nil && err != io.EOF {
		t.Errorf("ReadNBytes Error: %s", err)
	}

	if string(full) != str {
		t.Error("ReadNBytes Error, different string")
	}

	testBuf.Write([]byte(str))
	partial, err := ReadNBytes(&testBuf, 5)
	t.Logf("%s", partial)

	if err != nil {
		t.Errorf("ReadNBytes Error: %s", err)
	}

	if string(partial) != str[:5] {
		t.Error("ReadNBytes Error, different string")
	}
}

func TestWriteNBytes(t *testing.T) {
	str := "Hello world, I'm Jim"
	lens := 20

	var testWrite bytes.Buffer
	err := WriteNBytes(&testWrite, lens, []byte(str))

	if err != nil && err != io.EOF {
		t.Errorf("WriteNBytes Error: %s", err)
	}

	if string(testWrite.Bytes()) != str {
		t.Errorf("WriteNBytes Error, different string")
	}
}