package gota

import (
	"testing"
	"bytes"
)

func TestGotaFrame_IsControl(t *testing.T) {
	gf := &GotaFrame{
		Control: true,
	}
	if ! gf.IsControl() {
		t.Error("IsControl func return false, should return true")
	}

	gf = &GotaFrame{
		Control: false,
	}
	if gf.IsControl() {
		t.Error("IsControl func return true, should return false")
	}
}

func TestGotaFrame_MarshalBinary(t *testing.T) {
	output := []byte{0,0,0,0, 1,0,0,0, 2,0, 48,48}
	buf := []byte("00")
	gf := &GotaFrame{
		Control: false,
		SeqNum: 1,
		Length: 2,
		Payload: buf,
	}
	t.Logf("GotaFrame: %+v", gf)
	data, err := gf.MarshalBinary()
	if err != nil {
		t.Errorf("MarshalBinary Error: %s", err)
	}

	if bytes.Compare(data, output) != 0{
		t.Errorf("MarshalBinary Error: mismatch bytes")
	}

	t.Logf("Marshal data: %+v", data)
}

func TestGotaFrame_UnmarshalBinary(t *testing.T) {
	buf := []byte{0,0,0,0, 0,0,0,0, 0,0}
	t.Logf("Test data bytes: %+v", buf)
	var gf GotaFrame
	err := gf.UnmarshalBinary(buf)
	if err != nil && err != HeaderOnly {
		t.Errorf("UnmarshalBinary Error: %s", err)
	}

}

func TestTMHeartBeatBytes(t *testing.T) {
	var gf GotaFrame
	gf.UnmarshalBinary(TMHeartBeatBytes)

	t.Logf("GotaFrame: %+v", gf)
	if gf.ConnId == 0 && gf.Control && gf.Length == 0 && gf.SeqNum == TMHeartBeatSeq {
		t.Logf("Correct heartbeat bytes: %+v", TMHeartBeatBytes)
		return
	}
	t.Errorf("Error heartbeat bytes: %+v", TMHeartBeatBytes)
}

func TestTMCloseTunnelBytes(t *testing.T) {

}
