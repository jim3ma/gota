package gota

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
)

// GotaFrame struct
//
// First bit is Control flag, 1 for control, 0 for normal
// when control flag is 1, length must be 0
// ┏━━━━━━━┳━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┓
// ┃ 1 bit ┃    connection id: 31 bit      ┃ 4 bytes, 1 bit for control flag, and 31 bit for connection id
// ┣━━━━━━━┻━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━┫
// ┃        sequence number: 32 bit        ┃ 4 bytes for sequence number
// ┣━━━━━━━━━━━━━━━━━━━━┳━━━━━━━━━━━━━━━━━━┫
// ┃  length: 16 bit    ┃      data        ┃ 2 bytes for data length
// ┣━━━━━━━━━━━━━━━━━━━━┻━━━━━━━━━━━━━━━━━━┫
// ┃             data(continue)            ┃ data: length bit
// ┃                   ...                 ┃

type GotaFrame struct {
	// client ID, only used internal
	clientID uint32
	// Control
	Control bool
	// Connection ID, when ConnID > (1 << 31), this frame is a control frame
	ConnID uint32
	// Sequence number, when this frame is a control frame, SeqNum will be used from control
	SeqNum uint32
	// Data length
	Length int
	// Data
	Payload []byte
}

// String for logging
func (gf GotaFrame) String() string {
	return fmt.Sprintf("{ Control: %v, ConnID: %d, SeqNum: %d, Length: %d }", gf.Control, gf.ConnID, gf.SeqNum, gf.Length)
}

// IsControl return if this frame is a control type
func (gf GotaFrame) IsControl() bool {
	return gf.Control
}

// MarshalBinary generates GotaFrame to bytes
func (gf *GotaFrame) MarshalBinary() (data []byte, err error) {
	var buf bytes.Buffer

	cid := make([]byte, 4)
	if gf.Control {
		binary.LittleEndian.PutUint32(cid, gf.ConnID+ControlFlagBit)
	} else {
		binary.LittleEndian.PutUint32(cid, gf.ConnID)
	}
	buf.Write(cid)

	seq := make([]byte, 4)
	binary.LittleEndian.PutUint32(seq, gf.SeqNum)
	buf.Write(seq)

	var l uint16
	if gf.Length == MaxDataLength {
		l = 0
	} else {
		l = uint16(gf.Length)
	}
	lens := make([]byte, 2)
	binary.LittleEndian.PutUint16(lens, l)
	buf.Write(lens)

	buf.Write(gf.Payload)
	data = buf.Bytes()
	return
}

// UnmarshalBinary parses from bytes and sets up GotaFrame
func (gf *GotaFrame) UnmarshalBinary(data []byte) error {
	if len(data) < HeaderLength {
		return ErrInsufficientData
	}

	cid := binary.LittleEndian.Uint32(data[:4])
	seq := binary.LittleEndian.Uint32(data[4:8])
	lens := binary.LittleEndian.Uint16(data[8:])
	var lenx int

	var ctrl bool
	if (cid & ControlFlagBit) != 0 {
		ctrl = true
		cid = cid - ControlFlagBit
	}

	if lens == 0 && ctrl == false {
		lenx = MaxDataLength
	} else {
		lenx = int(lens)
	}
	gf.Control = ctrl
	gf.Length = lenx
	gf.ConnID = cid
	gf.SeqNum = seq

	if !gf.Control && len(data) == HeaderLength {
		return HeaderOnly
	} else if gf.Control {
		return nil
	}
	gf.Payload = data[10:]
	if len(gf.Payload) != gf.Length {
		return ErrUnmatchedDataLength
	}
	return nil
}

const MaxDataLength = 64 * 1024
const MaxConnID = 1<<31 - 1
const ControlFlagBit = 1 << 31

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

const (
	TMConnBiuniqueMode = iota
	TMConnOverlapMode
	TMConnMultiBiuniqueMode
	TMConnMultiOverlapMode
)

var TMHeartBeatBytes []byte
var TMCloseTunnelBytes []byte

const HeaderLength = 10

var HeaderOnly = errors.New("Gota Header Only")
var ErrInsufficientData = errors.New("Error Header, Insufficent Data for GotaFrame Header")
var ErrUnmatchedDataLength = errors.New("Unmatched Data Length for GotaFrame")

func WrapGotaFrame(gf *GotaFrame) []byte {
	b, _ := gf.MarshalBinary()
	return b
}

func UnwrapGotaFrame(data []byte) *GotaFrame {
	var gf GotaFrame
	gf.UnmarshalBinary(data)
	return &gf
}

func init() {
	TMHeartBeatBytes = WrapGotaFrame(&GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMHeartBeatSeq),
	})
	TMCloseTunnelBytes = WrapGotaFrame(&GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMCloseTunnelSeq),
	})
}
