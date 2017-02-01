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
	// Control
	Control bool
	// Connection ID, when ConnId > (1 << 31), this frame is a control frame
	ConnId uint32
	// Sequence number, when this frame is a control frame, SeqNum will be used from control
	SeqNum uint32
	// Data length
	Length int
	// Data
	Data   []byte
}

// String for logging
func (gf GotaFrame) String() string {
	return fmt.Sprintf("{ ConnId: %d, SeqNum: %d, Length: %d }", gf.ConnId, gf.SeqNum, gf.Length)
}

// IsControl return if this frame is a control type
func (gf GotaFrame) IsControl() bool {
	return gf.Control
}

// TODO
func (gf *GotaFrame) MarshalBinary() (data []byte, err error) {
	var buf bytes.Buffer

	cid := make([]byte, 4)
	binary.LittleEndian.PutUint32(cid, gf.ConnId)
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

	buf.Write(gf.Data)
	data = buf.Bytes()
	return
}

// TODO
func (gf *GotaFrame) UnmarshalBinary(data []byte) error {
	if len(data) < HeaderLength {
		return ErrInsufficentData
	}

	cid := binary.LittleEndian.Uint32(data[:4])
	seq := binary.LittleEndian.Uint32(data[4:8])
	lens := binary.LittleEndian.Uint16(data[8:])
	var lenx int

	var ctrl bool
	if cid & ControlFlagBit {
		ctrl = true
		cid -= ControlFlagBit
	}

	if lens == 0 {
		lenx = MaxDataLength
	} else {
		lenx = int(lens)
	}
	gf.Control = ctrl
	gf.Length = lenx
	gf.ConnId = cid
	gf.SeqNum = seq

	if ! gf.Control && len(data) == HeaderLength {
		return HeaderOnly
	} else if gf.Control {
		return nil
	}
	gf.Data = data[10:]
	if len(gf.Data) != gf.Length {
		return ErrUnmatchedDataLength
	}
	return nil
}

const MaxDataLength  = 64 * 1024
const MaxConnID      = 1 << 31 - 1
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
var ErrInsufficentData = errors.New("Error Header, Insufficent Data for GotaFrame Header")
var ErrUnmatchedDataLength = errors.New("Unmatched Data Length for GotaFrame")

func WrapGotaFrame(data *GotaFrame) []byte {
	var buf bytes.Buffer

	cid := make([]byte, 4)
	binary.LittleEndian.PutUint32(cid, data.ConnId)
	buf.Write(cid)

	seq := make([]byte, 4)
	binary.LittleEndian.PutUint32(seq, data.SeqNum)
	buf.Write(seq)

	var l uint16
	if data.Length == MaxDataLength {
		l = 0
	} else {
		l = uint16(data.Length)
	}
	lens := make([]byte, 2)
	binary.LittleEndian.PutUint16(lens, l)
	buf.Write(lens)

	buf.Write(data.Data)
	return buf.Bytes()
}

func UnwrapGotaFrame(h []byte) *GotaFrame {
	cid := binary.LittleEndian.Uint32(h[:4])
	seq := binary.LittleEndian.Uint32(h[4:8])
	lens := binary.LittleEndian.Uint16(h[8:])
	var lenx int

	var ctrl bool
	if cid & ControlFlagBit {
		ctrl = true
		cid -= ControlFlagBit
	}

	if lens == 0 {
		lenx = MaxDataLength
	} else {
		lenx = int(lens)
	}

	return &GotaFrame{
		Control: ctrl,
		ConnId:  cid,
		Length:  lenx,
		SeqNum:  seq,
	}
}

func init() {
	TMHeartBeatBytes = WrapGotaFrame(&GotaFrame{
		Control: true,
		ConnId: uint32(0),
		Length: 0,
		SeqNum: uint32(TMHeartBeatSeq),
	})
	TMCloseTunnelBytes = WrapGotaFrame(&GotaFrame{
		Control: true,
		ConnId: uint32(0),
		Length: 0,
		SeqNum: uint32(TMCloseTunnelSeq),
	})
}