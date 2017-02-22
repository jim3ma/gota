package gota

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
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

	// client ID, only used for tunnel of gota internal
	clientID uint32
}

// String for logging
func (gf GotaFrame) String() string {
	if gf.Control{
		return fmt.Sprintf("{ Control Signal: %s, ConnID: %d, Length: %d }",
			TMControlSignalMap[gf.SeqNum], gf.ConnID, gf.Length)
	}
	return fmt.Sprintf("{ Control: %v, ConnID: %d, SeqNum: %d, Length: %d }",
		gf.Control, gf.ConnID, gf.SeqNum, gf.Length)
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
const TMHeartBeatTickerSecond = 900
const TMHeartBeatTimeOutSecond = 3000
const TMStatReportSecond = 30

const (
	TMControlStartSeq = iota

	TMHeartBeatPingSeq
	TMHeartBeatPongSeq

	TMCreateConnSeq
	TMCreateConnOKSeq
	TMCreateConnErrorSeq

	TMCloseConnSeq
	TMCloseConnForceSeq
	//TMCloseConnOKSeq

	TMCloseTunnelSeq
	TMCloseTunnelOKSeq

	TMWithoutPayload

	TMTunnelAuthSeq
	TMTunnelAuthOKSeq
)

const (
	TMConnBiuniqueMode = iota
	TMConnOverlapMode
	TMConnMultiBiuniqueMode
	TMConnMultiOverlapMode
)

var TMHeartBeatPingBytes []byte
var TMHeartBeatPingGotaFrame *GotaFrame
var TMHeartBeatPongBytes []byte
var TMHeartBeatPongGotaFrame *GotaFrame
var TMCloseTunnelBytes []byte
var TMCloseTunnelGotaFrame *GotaFrame
var TMCloseTunnelOKBytes []byte
var TMCloseTunnelOKGotaFrame *GotaFrame
var TMTunnelAuthOKBytes []byte
var TMTunnelAuthOKGotaFrame *GotaFrame

var TMControlSignalMap map[uint32]string

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

func ReadGotaFrame(r io.Reader) (*GotaFrame, error) {
	header, err := ReadNBytes(r, HeaderLength)
	if err != nil {
		return nil, err
	}

	var gf GotaFrame
	err = gf.UnmarshalBinary(header)
	if err != nil && err != HeaderOnly {
		return nil, err
	}

	if gf.Control && gf.SeqNum < TMWithoutPayload {
		return &gf, nil
	}

	payload, err := ReadNBytes(r, gf.Length)
	if err != nil {
		return nil, err
	}
	gf.Payload = payload
	return &gf, nil
}

func EmbedClientIDHeaderToPayload(gf *GotaFrame) {
	client := make([]byte, 4)
	binary.LittleEndian.PutUint32(client, gf.clientID)

	payload := make([]byte, 0, gf.Length+4)
	payload = append(payload, client...)
	payload = append(payload, gf.Payload...)

	gf.Payload = payload
	gf.Length += 4
}

func ParseClientIDHeaderFromPayload(gf *GotaFrame) {
	client := binary.LittleEndian.Uint32(gf.Payload[:4])

	gf.Payload = gf.Payload[4:]
	gf.Length -= 4
	gf.clientID = client
}

func init() {
	TMHeartBeatPingGotaFrame = &GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMHeartBeatPingSeq),
	}
	TMHeartBeatPingBytes = WrapGotaFrame(TMHeartBeatPingGotaFrame)

	TMHeartBeatPongGotaFrame = &GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMHeartBeatPongSeq),
	}
	TMHeartBeatPongBytes = WrapGotaFrame(TMHeartBeatPongGotaFrame)

	TMCloseTunnelGotaFrame = &GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMCloseTunnelSeq),
	}
	TMCloseTunnelBytes = WrapGotaFrame(TMCloseTunnelGotaFrame)

	TMCloseTunnelOKGotaFrame = &GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMCloseTunnelOKSeq),
	}
	TMCloseTunnelOKBytes = WrapGotaFrame(TMCloseTunnelOKGotaFrame)

	TMTunnelAuthOKGotaFrame = &GotaFrame{
		Control: true,
		ConnID:  uint32(0),
		Length:  0,
		SeqNum:  uint32(TMTunnelAuthOKSeq),
	}
	TMTunnelAuthOKBytes = WrapGotaFrame(TMTunnelAuthOKGotaFrame)


	TMControlSignalMap = make(map[uint32]string,8)

	TMControlSignalMap[TMHeartBeatPingSeq]="HeartBeatPing"
	TMControlSignalMap[TMHeartBeatPongSeq]="HeartBeatPong"
	TMControlSignalMap[TMCreateConnSeq]="CreateConn"
	TMControlSignalMap[TMCreateConnOKSeq]="CreateConnOK"
	TMControlSignalMap[TMCreateConnErrorSeq]="CreateConnError"
	TMControlSignalMap[TMCloseConnSeq]="CloseConn"
	TMControlSignalMap[TMCloseConnForceSeq]="CloseConnForce"
	TMControlSignalMap[TMCloseTunnelSeq]="CloseTunnel"
	TMControlSignalMap[TMCloseTunnelOKSeq]="CloseTunnelOK"
	TMControlSignalMap[TMTunnelAuthSeq]="TunnelAuth"
	TMControlSignalMap[TMTunnelAuthOKSeq]="TunnelAuthOK"
}
