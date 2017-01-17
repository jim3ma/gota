package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
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
	connCloseChannel chan<- *net.TCPConn
	sendChannel      chan<- dataFrame
	receiveChannel   <-chan dataFrame
	// forward traffic to special conn
	receiveChanPool map[uint16]chan<- dataFrame
}

func newConnManager(in <-chan *net.TCPConn, sendChannel chan dataFrame, receiveChannel chan dataFrame) *ConnManager {
	close := make(chan *net.TCPConn)
	c := &ConnManager{
		nextConnID:       1,
		connChannel:      in,
		connCloseChannel: close,
		sendChannel:      sendChannel,
		receiveChannel:   receiveChannel,
	}
	go c.handleConn()
	return c
}

func (c *ConnManager) handleConn() {
	go c.closeConn()
	for conn := range c.connChannel {
		//c.mutex.Lock()
		receiveChannel := make(chan dataFrame)
		c.receiveChanPool[c.nextConnID] = receiveChannel
		ch := ConnHandler{
			cid: c.nextConnID,
			conn: conn,
			connCloseChannel: c.connCloseChannel,
			sendChannel: c.sendChannel,
			receiveChannel: receiveChannel,
		}
		go ch.start()
		if c.nextConnID == MaxConnID {
			c.nextConnID = 1
		} else {
			c.nextConnID += 1
		}
		//c.mutex.Unlock()
	}
}

func (c *ConnManager) receive() {
	for d := range c.receiveChannel {
		if ch, ok := c.receiveChanPool[d.ConnId]; ok {
			ch <- d
		} else {
			// TODO
		}
	}
}

func (c *ConnManager) closeConn() {
	for conn := range c.connCloseChannel {
		err := conn.Close()
		if err != nil {
			log.Errorf("Close Connection Error: %s", err)
		}
	}
}

type ConnHandler struct {
	cid              uint16
	conn             *net.TCPConn
	connCloseChannel chan<- *net.TCPConn
	sendChannel      chan<- dataFrame
	receiveChannel   <-chan dataFrame
}

func (ch *ConnHandler) start() {
	go ch.send()
	go ch.receive()
}

func (ch *ConnHandler) send(){
	defer func() {
		if r := recover(); r != nil {
			log.Error("Read data failed: %v", r)
		}
		ch.connCloseChannel <- ch.conn
	}()

	var seq uint32
	seq = 1
	for {
		data := make([]byte, MaxDataLength)
		_, err := ch.conn.Read(data)
		if err != nil && err != io.EOF {
			panic(err)
		}
		df := dataFrame{
			ConnId: ch.cid,
			SeqNum: seq,
			Length: uint16(len(data)),
			data:   data,
		}
		seq += 1
		ch.sendChannel <- df
	}
}

func (ch *ConnHandler) receive(){
	defer func(){
		if r := recover(); r != nil {
			log.Error("Write data failed: %v", r)
		}
	}()

	var seq uint32
	seq = 1
	for d := range ch.receiveChannel{
		if d.SeqNum == seq {
			_, err := ch.conn.Write(d.data)
			if err != nil {
				panic(err)
			}
		}
		// TODO cache for disorder
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
	localIPs       []string
	remoteAddrs    []string
	workerPool     []TunnelWorker
	mode           int
}

func newTunnelManager(send chan dataFrame, receive chan dataFrame, mode int, localIPs []string, remoteAddrs []string) *TunnelManager {
	c := &TunnelManager{
		sendChannel:    send,
		receiveChannel: receive,
		mode:           mode,
		localIPs:       localIPs,
		remoteAddrs:    remoteAddrs,
	}
	go c.start()
	return c
}

func (c *TunnelManager) start() {
	// TODO
	// multi mode support
	switch c.mode {
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
	for _, lip := range c.localIPs {
		for _, rAddr := range c.remoteAddrs {
			log.Info("local IP: %s, remote Address: %s", lip, rAddr)
		}
	}
}

type TunnelWorker struct {
	//SWokerPool chan *TunnelWorker
	//RWokerPool chan *TunnelWorker
	localAddr     string
	remoteAddr    string
	cancel        int
	heartbeatChan chan int
	// read from client and send to server
	sendChannel <-chan dataFrame
	// receive from server and send to client
	receiveChannel chan<- dataFrame
	//retryTime int
}

func (cw *TunnelWorker) heartbeat() {
	for {
		select {
		case <-time.After(time.Second * TMHeartBeatSecond):
			cw.heartbeatChan <- 0
			if cw.cancel == -1 {
				break
			}
		}
	}
}

func (cw *TunnelWorker) start() {
	// connect server
	lAddr, err := net.ResolveTCPAddr("ip", cw.localAddr+":0")
	if err != nil {
		log.Fatalf("Using Local Address: %s, Error: %s", lAddr, err)
		return
	}
	rAddr, err := net.ResolveTCPAddr("ip", cw.remoteAddr)
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

	sendDone, receiveDone, cancel := make(chan int), make(chan int), make(chan int)
	go cw.send(sendDone, cancel, conn, cw.sendChannel)
	go cw.receive(receiveDone, conn, cw.receiveChannel)
	<-sendDone
	<-receiveDone
}

func (cw *TunnelWorker) resetart() {
	cw.cancel = 0
	go cw.start()
}

// traffic -> gota client -> internet -> gota server -> ...
func (cw *TunnelWorker) send(done <-chan int, cancel <-chan int, conn *net.TCPConn, in <-chan dataFrame) {
	for {
		// register to pool
		//cw.SWokerPool <- cw
		select {
		case d := <-in:
			_, err := conn.Write(wrapDataFrame(d))
			if err != nil {
				panic(err)
			}
		case <-cancel:
			log.Infof("Shutdown Worker: %v", cw)
			_, err := conn.Write([]byte(TMCloseConnString))
			if err != nil {
				log.Fatalf("Send Close Signal failed duo to: %s", err)
			}
			//cw.cancel = -1
			return
		case <-cw.heartbeatChan:
			_, err := conn.Write([]byte(TMHeartBeatString))
			if err != nil {
				log.Fatalf("HeartBeat failed duo to: %s", err)
				//cw.cancel = -1
				return
			}
		}
	}
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Runtime error caught: %v", r)
		}
		cw.cancel = -1
		done <- 0
	}()
}

// traffic <- gota client <- internet <- gota server <- ...
func (cw *TunnelWorker) receive(done <-chan int, conn *net.TCPConn, out chan<- dataFrame) {
	for {
		// register to pool
		//cw.RWokerPool <- cw
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
		out <- df

		if cw.cancel == -1 || err == io.EOF {
			// reset cancel flag
			cw.cancel = 0
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
	buf.Write([]byte(data.ConnId))
	buf.Write([]byte(data.Length))
	buf.Write([]byte(data.SeqNum))
	buf.Write([]byte(data.data))
	return buf.Bytes()
}

func unWrapDataFrame(h []byte) dataFrame {
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
}

type dataFrame struct {
	// Connection ID
	ConnId uint16
	Length uint16
	// Sequence Number
	SeqNum uint32
	data   []byte
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Fatalf("Runtime error caught: %v", r)
		}
	}()

	// TODO
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
	newConnManager(newConnChannel, sendChannel, receiveChannel)
	newTunnelManager(sendChannel, receiveChannel, TMConnBiuniqueMode, []string{"127.0.0.1", "127.0.0.1"}, []string{"127.0.0.1:8080", "127.0.0.1:8080"})

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			//panic(err)
			// TODO
			log.Fatalf("Accept Connection Error: %s", err)
			continue
		}
		newConnChannel <- conn
	}
}
