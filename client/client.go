package main

import (
	"bytes"
	"encoding/binary"
	//"fmt"
	log "github.com/Sirupsen/logrus"
	"io"
	"net"
	//"sync"
	"time"
)

type ConnManager struct {
	//mutex            *sync.Mutex
	nextConnID       uint16
	connChannel      <-chan *net.TCPConn
	connCloseChannel chan uint16
	sendChannel      chan<- dataFrame
	receiveChannel   <-chan dataFrame
	connectionPool   map[uint16]*ConnHandler
}

func newConnManager(in <-chan *net.TCPConn, sendChannel chan dataFrame, receiveChannel chan dataFrame) *ConnManager {
	close := make(chan uint16)
	c := &ConnManager{
		nextConnID:       1,
		connChannel:      in,
		connCloseChannel: close,
		sendChannel:      sendChannel,
		receiveChannel:   receiveChannel,
	}
	//go c.handleConn()
	return c
}

// receive connection for local port, and dispatch a Connection Handler to forward traffic
func (c *ConnManager) handleConn() {
	go c.closeConn()
	for conn := range c.connChannel {
		//t.mutex.Lock()
		receiveChannel := make(chan dataFrame)
		ch := ConnHandler{
			cid:              c.nextConnID,
			conn:             conn,
			connCloseChannel: c.connCloseChannel,
			sendChannel:      c.sendChannel,
			receiveChannel:   receiveChannel,
		}
		// all handlers share one send channel, and every handler uses one receive channel,
		// we need register the receive channel, so we can forward traffic from tunnels to local connection
		c.connectionPool[c.nextConnID] = &ch
		go ch.start()
		if c.nextConnID == MaxConnID {
			c.nextConnID = 1
		} else {
			c.nextConnID += 1
		}
		//t.mutex.Unlock()
	}
}

// receive from receiveChannel and forward to special receive channel according the connection ID
func (c *ConnManager) receive() {
	for d := range c.receiveChannel {
		if ch, ok := c.connectionPool[d.ConnId]; ok {
			ch.receiveChannel <- d
		} else {
			log.Errorf("Connection didn't exist, connection id: %d", d.ConnId)
		}
	}
}

func (c *ConnManager) closeConn() {
	for cid := range c.connCloseChannel {
		err := c.connectionPool[cid].conn.Close()
		if err != nil {
			log.Errorf("Close Connection Error: %s", err)
		}
		delete(c.connectionPool, cid)
	}
}

type ConnHandler struct {
	cid              uint16
	conn             *net.TCPConn
	connCloseChannel chan<- uint16
	sendChannel      chan<- dataFrame
	receiveChannel   chan dataFrame
}

func (ch *ConnHandler) start() {
	go ch.send()
	go ch.receive()
}

func (ch *ConnHandler) send() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Read data failed: %v", r)
		}
		ch.connCloseChannel <- ch.cid
	}()

	var seq uint32
	seq = 1
	for {
		data := make([]byte, MaxDataLength)
		_, err := ch.conn.Read(data)
		if err != nil && err != io.EOF {
			panic(err)
		}
		if len(data) > 0 {
			df := dataFrame{
				ConnId: ch.cid,
				SeqNum: seq,
				Length: uint16(len(data)),
				data:   data,
			}
			seq += 1
			ch.sendChannel <- df
		} else {
			log.Error("Read mpty data")
		}
		if err == io.EOF {
			return
		}
	}
}

func (ch *ConnHandler) receive() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Write data failed: %v", r)
		}
	}()

	var seq uint32
	seq = 1
	cache := make(map[uint32][]byte)
	for d := range ch.receiveChannel {
		if d.SeqNum == seq {
			_, err := ch.conn.Write(d.data)
			if err != nil && err != io.EOF {
				panic(err)
			}
			if err == io.EOF {
				return
			}
			seq += 1

			if len(cache) == 0 {
				continue
			}
			// TODO check cache and send to client
			for {
				data, ok := cache[seq]
				if ok {
					_, err := ch.conn.Write(data)
					if err != nil && err != io.EOF {
						panic(err)
					}
					if err == io.EOF {
						return
					}
					delete(cache, seq)
					seq += 1
				}
				break
			}
		} else if d.SeqNum > seq {
			// TODO cache for disorder data frame
			cache[d.SeqNum] = d.data
		}
	}
}

// connection pool
type TunnelManager struct {
	// Send Woker Pool
	//SWokerPool chan *TunnelWorker
	// Receive Worker pool
	//RWokerPool chan *TunnelWorker
	sendChannel    <-chan dataFrame
	receiveChannel chan<- dataFrame
	cancelChannel  chan int
	localIPs       []string
	remoteAddrs    []string
	workerPool     []*TunnelWorker
	mode           int
}

func newTunnelManager(send chan dataFrame, receive chan dataFrame, cancel chan int, mode int, localIPs []string, remoteAddrs []string) *TunnelManager {
	t := &TunnelManager{
		sendChannel:    send,
		receiveChannel: receive,
		cancelChannel:  cancel,
		localIPs:       localIPs,
		remoteAddrs:    remoteAddrs,
		mode:           mode,
	}
	//go t.start()
	return t
}

func (t *TunnelManager) start() {
	// TODO multi mode support
	switch t.mode {
	case TMConnBiuniqueMode:
		log.Info("Work Mode: Biunique")
	case TMConnOverlapMode:
		log.Info("Work Mode: Overlap")
	case TMConnMultiBiuniqueMode:
		log.Info("Work Mode: Multi Biunique")
	case TMConnMultiOverlapMode:
		log.Info("Work Mode: Multi Overlap")
	default:
		log.Error("Unknown Worker Mode")
		panic("Unknown Worker Mode")
	}
	for _, lAddr := range t.localIPs {
		for _, rAddr := range t.remoteAddrs {
			log.Info("lAddr IP: %s, remote Address: %s", lAddr, rAddr)
			heartbeat := make(chan int)
			tw := &TunnelWorker{
				localAddr:  lAddr,
				remoteAddr: rAddr,
				cancelChannel: t.cancelChannel,
				heartbeatChan: heartbeat,
				c2sChannel: t.sendChannel,
				s2cChannel: t.receiveChannel,
			}
			tw.start()
		}
	}
}

type TunnelWorker struct {
	//SWokerPool chan *TunnelWorker
	//RWokerPool chan *TunnelWorker
	localAddr     string
	remoteAddr    string
	cancelFlag    int
	cancelChannel chan int
	heartbeatChan chan int
	// read from client and send to server
	c2sChannel <-chan dataFrame
	// receive from server and send to client
	s2cChannel chan<- dataFrame
	//retryTime int
}

func (tw *TunnelWorker) heartbeat() {
	for {
		select {
		case <-time.After(time.Second * TMHeartBeatSecond):
			tw.heartbeatChan <- 0
			if tw.cancelFlag == -1 {
				break
			}
		}
	}
}

func (tw *TunnelWorker) start() {
	// connect server
	lAddr, err := net.ResolveTCPAddr("ip", tw.localAddr+":0")
	if err != nil {
		log.Fatalf("Using Local Address: %s, Error: %s", lAddr, err)
		return
	}
	rAddr, err := net.ResolveTCPAddr("ip", tw.remoteAddr)
	if err != nil {
		log.Fatalf("Using Remote Address: %s, Error: %s", rAddr, err)
		return
	}

	conn, err := net.DialTCP("tcp", lAddr, rAddr)
	if err != nil {
		log.Fatalf("Connect to Server: %s, using Local Address: %s, Error: %s", rAddr, lAddr, err)
		return
	}
	defer conn.Close()

	sendDone, receiveDone := make(chan int), make(chan int)
	go tw.send(sendDone, conn)
	go tw.receive(receiveDone, conn)
	log.Infof("TunnelWorker start to forward traffic, local address: %s, remote address: %s", tw.localAddr, tw.remoteAddr)
	<-sendDone
	<-receiveDone
}

func (tw *TunnelWorker) resetart() {
	tw.cancelFlag = 0
	go tw.start()
}

// traffic -> gota client -> internet -> gota server -> ...
func (tw *TunnelWorker) send(done chan<- int, conn *net.TCPConn) {
	for {
		// register to pool
		//tw.SWokerPool <- tw
		select {
		case d := <-tw.c2sChannel:
			_, err := conn.Write(wrapDataFrame(d))
			if err != nil {
				panic(err)
			}
		case <-tw.cancelChannel:
			log.Infof("Shutdown Worker: %v", tw)
			_, err := conn.Write([]byte(TMCloseConnString))
			if err != nil {
				log.Fatalf("Send Close Signal failed duo to: %s", err)
			}
			//tw.cancelFlag = -1
			return
		case <-tw.heartbeatChan:
			_, err := conn.Write([]byte(TMHeartBeatString))
			if err != nil {
				log.Fatalf("HeartBeat failed duo to: %s", err)
				//tw.cancelFlag = -1
				return
			}
		}
	}
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Runtime error caught: %v", r)
		}
		tw.cancelFlag = -1
		done <- 0
	}()
}

// traffic <- gota client <- internet <- gota server <- ...
func (tw *TunnelWorker) receive(done chan<- int, conn *net.TCPConn) {
	for {
		// register to pool
		//tw.RWokerPool <- tw
		header := make([]byte, 8)
		n, err := conn.Read(header)
		if (err != nil && err != io.EOF) || n != 8 {
			panic(err)
		}
		if err == io.EOF {
			break
		}

		df := unWrapDataFrame(header)
		if df.Length == 0 && df.ConnId == 0 {
			if df.SeqNum == TMHeartBeatSeq {
				log.Debug("Received HeartBeat Signal")
				continue
			} else if df.SeqNum == TMCloseConnSeq {
				log.Debug("Received Close Connection Signal")
			}
			log.Errorf("Unkownn Signal: %d", df.SeqNum)
		}

		data := make([]byte, MaxDataLength)
		n, err = conn.Read(data)
		if (err != nil && err != io.EOF) || n != int(df.Length) {
			panic(err)
		}
		df.data = data
		tw.s2cChannel <- df

		if tw.cancelFlag == -1 || err == io.EOF {
			// reset cancelFlag flag
			tw.cancelFlag = 0
			break
		}
	}
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Runtime error caught: %v", r)
		}
		done <- 0
	}()
}

const MaxDataLength = 65536
const MaxConnID = 65535

// Connection Manage HeartBeat Time
const TMHeartBeatSecond = 15
const TMHeartBeatString = "00000000"
const TMHeartBeatSeq = 0
const TMCloseConnString = "00000001"
const TMCloseConnSeq = 1

const (
	TMConnBiuniqueMode = iota
	TMConnOverlapMode
	TMConnMultiBiuniqueMode
	TMConnMultiOverlapMode
)

func wrapDataFrame(data dataFrame) []byte {
	var buf bytes.Buffer

	cid := make([]byte, 2)
	binary.LittleEndian.PutUint16(cid, data.ConnId)
	buf.Write(cid)

	len := make([]byte, 2)
	binary.LittleEndian.PutUint16(len, data.Length)
	buf.Write(len)

	seq := make([]byte, 4)
	binary.LittleEndian.PutUint32(seq, data.SeqNum)
	buf.Write(seq)

	buf.Write(data.data)
	return buf.Bytes()
}

func unWrapDataFrame(h []byte) dataFrame {
	cid := binary.LittleEndian.Uint16(h[:2])
	len := binary.LittleEndian.Uint16(h[2:4])
	seq := binary.LittleEndian.Uint32(h[4:])
	return dataFrame{
		ConnId: cid,
		Length: len,
		SeqNum: seq,
	}
	/*
		cid, n := binary.Uvarint(h[:2])
		if n <= 0 {
			panic(fmt.Sprintf("Error Data Frame Header: %s", h))
		}

		len, n := binary.Uvarint(h[2:4])
		if n <= 0 {
			panic(fmt.Sprintf("Error Data Frame Header: %s", h))
		}

		seq, n := binary.Uvarint(h[4:])
		if n <= 0 {
			panic(fmt.Sprintf("Error Data Frame Header: %s", h))
		}
		return dataFrame{
			uint16(cid),
			uint16(len),
			uint32(seq),
			nil}
	*/
}

type dataFrame struct {
	// Connection ID
	ConnId uint16
	// Data length
	Length uint16
	// Sequence number
	SeqNum uint32
	data   []byte
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Runtime error caught: %v", r)
		}
	}()

	// TODO configuration
	localAddr := "localhost:8081"
	//remoteAddr := "localhost:1081"

	addr, err := net.ResolveTCPAddr("tcp", localAddr)
	if err != nil {
		panic(err)
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}

	newConnChannel := make(chan *net.TCPConn)

	sendChannel, receiveChannel := make(chan dataFrame), make(chan dataFrame)
	cm := newConnManager(newConnChannel, sendChannel, receiveChannel)
	go cm.handleConn()

	cancelChannel := make(chan int)
	tm := newTunnelManager(sendChannel, receiveChannel, cancelChannel, TMConnBiuniqueMode, []string{"127.0.0.1", "127.0.0.1"}, []string{"127.0.0.1:8080", "127.0.0.1:8080"})
	tm.start()

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			//panic(err)
			// TODO error handle which accept new connection
			log.Fatalf("Accept Connection Error: %s", err)
			continue
		}
		newConnChannel <- conn
	}
}
