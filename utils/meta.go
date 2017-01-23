package utils

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// GotaFrame
// TODO client id for different tunnel group
type GotaFrame struct {
	// Connection ID
	ConnId uint16

	// Sequence number
	SeqNum uint32

	// Data length
	Length uint16
	Data   []byte
}

func (gf GotaFrame) String() string {
	if gf.Length > GotaFrameDebugDataLength && len(gf.Data) > 0 {
		return fmt.Sprintf("\n{\n  ConnId: %d, \n  SeqNum: %d, \n  Length: %d, \n  Data(%d of %d bytes): \n    %+v\n}",
			gf.ConnId, gf.SeqNum, gf.Length, GotaFrameDebugDataLength,
			gf.Length, gf.Data[:GotaFrameDebugDataLength] )
	}
	return fmt.Sprintf("\n{\n  ConnId: %d, \n  SeqNum: %d, \n  Length: %d, \n  Data  : %+v\n}",
			gf.ConnId, gf.SeqNum, gf.Length, gf.Data)
}

const GotaFrameDebugDataLength = 256

const MaxDataLength = 32 * 1024
const MaxConnID = 64 * 1024 - 1

const CtrlFrameLength  = 0

// Connection Manage HeartBeat Time
const TMHeartBeatSecond = 300
const TMStatReportSecond = 30

const (
	TMHeartBeatSeq = iota
	TMCreateConnSeq
	TMCreateConnOKSeq
	TMCloseConnSeq
	TMCloseConnOKSeq
	TMCloseTunnelSeq
)

var TMHeartBeatBytes []byte
var TMCloseTunnelBytes []byte

func init() {
	TMHeartBeatBytes = WrapDataFrame(GotaFrame{
		ConnId: uint16(0),
		Length: uint16(0),
		SeqNum: uint32(TMHeartBeatSeq),
	})
	TMCloseTunnelBytes = WrapDataFrame(GotaFrame{
		ConnId: uint16(0),
		Length: uint16(0),
		SeqNum: uint32(TMCloseTunnelSeq),
	})
}


const (
	TMConnBiuniqueMode = iota
	TMConnOverlapMode
	TMConnMultiBiuniqueMode
	TMConnMultiOverlapMode
)

func WrapDataFrame(data GotaFrame) []byte {
	var buf bytes.Buffer

	cid := make([]byte, 2)
	binary.LittleEndian.PutUint16(cid, data.ConnId)
	buf.Write(cid)

	lens := make([]byte, 2)
	binary.LittleEndian.PutUint16(lens, data.Length)
	buf.Write(lens)

	seq := make([]byte, 4)
	binary.LittleEndian.PutUint32(seq, data.SeqNum)
	buf.Write(seq)

	buf.Write(data.Data)
	return buf.Bytes()
}

func UnwrapDataFrame(h []byte) GotaFrame {
	cid := binary.LittleEndian.Uint16(h[:2])
	lens := binary.LittleEndian.Uint16(h[2:4])
	seq := binary.LittleEndian.Uint32(h[4:])
	return GotaFrame{
		ConnId: cid,
		Length: lens,
		SeqNum: seq,
	}
}