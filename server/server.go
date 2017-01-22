package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"
	_ "net/http/pprof"
	"github.com/jim3ma/gota/utils"
	log "github.com/Sirupsen/logrus"
)

type ConnManager struct {
	//mutex            *sync.Mutex
	connIdChannel    <-chan uint16
	connChannel      <-chan *net.TCPConn
	connCloseChannel chan uint16
	r2sChannel       chan<- utils.GotaFrame
	s2rhannel        <-chan utils.GotaFrame
	connectionPool   map[uint16]*ConnHandler
	remoteAddr       string
}

func newConnManager(cid <-chan uint16, r2s chan utils.GotaFrame, s2r chan utils.GotaFrame, rAddr string) *ConnManager {
	closeChannel := make(chan uint16)
	connPool := make(map[uint16]*ConnHandler)
	c := &ConnManager{
		connIdChannel:    cid,
		connCloseChannel: closeChannel,
		r2sChannel:       r2s,
		s2rhannel:        s2r,
		remoteAddr:       rAddr,
		connectionPool:   connPool,
	}
	//go c.handleConn()
	return c
}

// c2s connection for local port, and dispatch a Connection Handler to forward traffic
func (c *ConnManager) handleConn() {
	go c.closeConn()
	go c.dispatch()
	for cid := range c.connIdChannel {
		//t.mutex.Lock()
		s2rChannel := make(chan utils.GotaFrame)
		ch := ConnHandler{
			cid:              cid,
			connCloseChannel: c.connCloseChannel,
			r2sChannel:       c.r2sChannel,
			s2rChannel:       s2rChannel,
			remoteAddr:       c.remoteAddr,
		}
		// all handlers share one s2c channel, and every handler uses one c2s channel,
		// we need register the c2s channel, so we can forward traffic from tunnels to local connection
		c.connectionPool[cid] = &ch
		go ch.start()
		//t.mutex.Unlock()
	}
}

// send Magic number to server, than server will create a new connection
//func (c *ConnManager) createConnOnServer(cid uint16){
//	c.r2sChannel <-
//}

// reveive from s2rChannel and forward to special c2s channel according the connection ID
func (c *ConnManager) dispatch() {
	for d := range c.s2rhannel {
		if ch, ok := c.connectionPool[d.ConnId]; ok {
			ch.s2rChannel <- d
		} else {
			log.Errorf("Connection didn't exist, connection id: %d, dropped", d.ConnId)
		}
	}
}

func (c *ConnManager) closeConn() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Close connection error: %s", r)
			log.Errorf("Call stack: %s", debug.Stack())

			go c.closeConn()
		}
	}()
	for cid := range c.connCloseChannel {
		// TODO runtime error
		// invalid memory address or nil pointer dereference
		conn := c.connectionPool[cid].conn
		log.Debugf("Try to close connection from server(%v) to remote(%v)", conn.LocalAddr(), conn.RemoteAddr())
		err := conn.Close()
		if err != nil {
			log.Errorf("Close connection Error: %s", err)
		}
		log.Debugf("Closed connection from server(%v) to remote(%v)", conn.LocalAddr(), conn.RemoteAddr())
		delete(c.connectionPool, cid)
	}
}

type ConnHandler struct {
	cid              uint16
	conn             *net.TCPConn
	connCloseChannel chan<- uint16
	// s2c to client
	r2sChannel chan<- utils.GotaFrame
	// c2s from client
	s2rChannel chan utils.GotaFrame
	remoteAddr string
	writeClosed      bool
	readClosed       bool
}

func (ch *ConnHandler) start() {
	// create connection to remote
	rAddr, err := net.ResolveTCPAddr("tcp", ch.remoteAddr)
	if err != nil {
		log.Debugf("Create new connection to remote error: %s", err)
		return
	}
	ch.conn, err = net.DialTCP("tcp", nil, rAddr)
	if err != nil {
		log.Debugf("Create new connection to remote error: %s", err)
		return
	}

	// s2c response
	resp := utils.GotaFrame{
		ConnId: ch.cid,
		Length: 0,
		SeqNum: uint32(utils.TMCreateConnOKSeq),
	}
	log.Debugf("Created a peer connection on server, connection id: %d", ch.cid)
	ch.r2sChannel <- resp

	go ch.r2s()
	go ch.s2r()
}

// read from remote and s2c to client
func (ch *ConnHandler) r2s() {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Read data failed: %v", r)
			log.Errorf("Call stack: %s", debug.Stack())
		}
		ch.connCloseChannel <- ch.cid
	}()

	var seq uint32
	seq = 1
	for {
		data := make([]byte, utils.MaxDataLength)
		n, err := ch.conn.Read(data)
		if n > 0 {
			log.Debugf("Received %d bytes from remote", n)

			log.Debugf("Received bytes from remote, connection id: %d, seq: %d, wrote length: %d", ch.cid, seq, n)
			df := utils.GotaFrame{
				ConnId: ch.cid,
				SeqNum: seq,
				Length: uint16(n),
				Data:   data[:n],
			}
			seq += 1
			ch.r2sChannel <- df
		} else {
			log.Warn("Received empty data from remote")
		}
		if err == io.EOF {
			log.Debugf("Received io.EOF from x(%v), start to close write connection on client ", ch.conn.RemoteAddr())
			// close write connection between client and x
			ch.r2sChannel <- utils.GotaFrame{
				ConnId: uint16(ch.cid),
				Length: uint16(0),
				SeqNum: uint32(utils.TMCloseConnSeq),
			}
			ch.conn.CloseRead()
			ch.readClosed = true
			break
		}
		if err != nil {
			panic(err)
		}
	}
}

// read from client, and s2c to remote
func (ch *ConnHandler) s2r() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Write data failed: %v", r)
			log.Errorf("Call stack: %s", debug.Stack())
		}
	}()

	var seq uint32
	seq = 1
	cache := make(map[uint32][]byte)
	for d := range ch.s2rChannel {
		if d.Length == utils.CtrlFrameLength {
			if d.SeqNum == utils.TMCloseConnSeq {
				log.Debugf("Received close write connection signal, try to close write connection")
				ch.conn.CloseWrite()
				ch.writeClosed = true
				return
			}
		}
		if d.SeqNum == seq {
			n, err := ch.conn.Write(d.Data)
			if err != nil && err != io.EOF {
				panic(err)
			}
			log.Debugf("Wrote data to remote, connection id: %d, seq: %d, length: %d, wrote length: %d", d.ConnId, d.SeqNum, d.Length, n)
			if err == io.EOF {
				break
			}
			seq += 1

			if len(cache) == 0 {
				continue
			}
			// TODO check cache and s2c to client
			for {
				if data, ok := cache[seq]; ok {
					_, err := ch.conn.Write(data)
					if err != nil && err != io.EOF {
						panic(err)
					}
					if err == io.EOF {
						return
					}
					delete(cache, seq)
					seq += 1
				} else {
					break
				}
			}
		} else if d.SeqNum > seq {
			// TODO cache for disorder data frame
			cache[d.SeqNum] = d.Data
		}
	}
}

// connection pool
type TunnelManager struct {
	// Send Woker Pool
	//SWokerPool chan *TunnelWorker
	// Receive Worker pool
	//RWokerPool chan *TunnelWorker
	connIdChannel chan<- uint16
	r2sChannel    <-chan utils.GotaFrame
	s2rChannel    chan<- utils.GotaFrame
	cancelChannel chan int
	localAddrs    []string
	//remoteAddr     string
	workerPool []*TunnelWorker
}

func newTunnelManager(cid chan uint16, r2s chan utils.GotaFrame, s2r chan utils.GotaFrame, cancel chan int, localAddrs []string /*, remoteAddr string*/) *TunnelManager {
	t := &TunnelManager{
		connIdChannel: cid,
		r2sChannel:    r2s,
		s2rChannel:    s2r,
		cancelChannel: cancel,
		localAddrs:    localAddrs,
		//remoteAddr:     remoteAddr,
	}
	//go t.start()
	return t
}

func (t *TunnelManager) start() {
	for _, lAddr := range t.localAddrs {
		log.Infof("Create a tunnel worker and bind local address IP: %s", lAddr /*, t.remoteAddr*/)
		heartbeat := make(chan int)
		tw := &TunnelWorker{
			localAddr: lAddr,
			//remoteAddr: t.remoteAddr,
			connIdChannel: t.connIdChannel,
			cancelChannel: t.cancelChannel,
			heartbeatChan: heartbeat,
			r2sChannel:    t.r2sChannel,
			s2rChannel:    t.s2rChannel,
		}
		tw.start()
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
	// create a new connection to remote
	connIdChannel chan<- uint16
	// read from client and s2c to server
	r2sChannel <-chan utils.GotaFrame
	// c2s from server and s2c to client
	s2rChannel chan<- utils.GotaFrame
	//retryTime int
}

func (tw *TunnelWorker) heartbeat() {
	for {
		select {
		case <-time.After(time.Second * utils.TMHeartBeatSecond):
			tw.heartbeatChan <- 0
			if tw.cancelFlag == -1 {
				break
			}
		}
	}
}

func (tw *TunnelWorker) start() {
	addr, err := net.ResolveTCPAddr("tcp", tw.localAddr)
	if err != nil {
		panic(err)
	}

	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}

	// TODO listener graceful shutdown
	s2cDone, c2sDone := make(chan int), make(chan int)
	//var wg sync.WaitGroup
	go func() {
		<-s2cDone
	}()

	go func() {
		<-c2sDone
	}()

	// TODO heartbeat should note share
	go tw.heartbeat()

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			//panic(err)
			// TODO error handle which accept new connection
			log.Errorf("Accept Connection Error: %s", err)
			continue
		}
		//wg.Add(1)
		log.Debugf("Accept a new tunnel connection from client(%s) to server(%s)", conn.RemoteAddr(), conn.LocalAddr())
		go tw.s2c(s2cDone, conn)
		go tw.c2s(c2sDone, conn)
	}
	//wg.Wait()
}

func (tw *TunnelWorker) resetart() {
	tw.cancelFlag = 0
	go tw.start()
}

// traffic <- gota client <- internet <- gota server <- ...
func (tw *TunnelWorker) s2c(done chan<- int, conn *net.TCPConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Runtime error caught: %v, runtime info: %s", r, utils.GoRuntimeInfo())
			log.Errorf("Call stack: %s", debug.Stack())
		}
		tw.cancelFlag = -1
		done <- 0
	}()
	for {
		select {
		case d := <-tw.r2sChannel:
			log.Debugf("Received data from remote: %+v", d)
			n, err := conn.Write(utils.WrapDataFrame(d))
			if err != nil {
				panic(err)
			}
			log.Debugf("Wrote data frame to tunnel, length: %d", n)
		case <-tw.cancelChannel:
			log.Infof("Shutdown worker: %v", tw)
			_, err := conn.Write(utils.TMCloseTunnelBytes)
			if err != nil {
				log.Fatalf("Send close signal failed duo to: %s", err)
			}
			//tw.cancelFlag = -1
			return
		case <-tw.heartbeatChan:
			_, err := conn.Write(utils.TMHeartBeatBytes)
			if err != nil {
				log.Errorf("Heartbeat failed duo to: %s", err)
				//tw.cancelFlag = -1
				return
			}
			log.Debugf("Sent heartbeat to client(%s) from server(%s)", conn.LocalAddr(), conn.RemoteAddr())
		}
	}
}

// traffic -> gota client -> internet -> gota server -> ...
func (tw *TunnelWorker) c2s(done chan<- int, conn *net.TCPConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Runtime error caught: %v, runtime info: %s", r, utils.GoRuntimeInfo())
			log.Errorf("Call stack: %s", debug.Stack())
		}
		done <- 0
	}()
	for {
		header := make([]byte, 8)
		n, err := conn.Read(header)
		if n == 0 && err == nil {
			log.Debug("Receive empth data, skip and continue")
			continue
		} else if err != io.EOF && (err != nil || n != 8) {
			log.Error("Receive data frame header error")
			//panic(err)
		}
		if err == io.EOF {
			log.Infof("Data frame header received io.EOF, stop this worker(client: %v, server: %v)",
				conn.RemoteAddr(), conn.LocalAddr())
			break
		}

		df := utils.UnwrapDataFrame(header)
		log.Debugf("Received data frame header from client: %+v", df)

		if df.Length == 0 {
			switch df.SeqNum {
			case utils.TMHeartBeatSeq:
				log.Debugf("Received heartbeat signal from client(%s) to server(%s)", conn.RemoteAddr(), conn.LocalAddr())
				continue
			case utils.TMCreateConnSeq:
				log.Debug("Received create connection signal")
				tw.connIdChannel <- df.ConnId
				continue
			case utils.TMCloseConnSeq:
				log.Debug("Received close connection signal")
				tw.s2rChannel <- df
				continue
			//case utils.TMCloseTunnelSeq:
			//	log.Info("Receive close tunnel signal")
				// TODO close tunnel
			default:
				log.Errorf("Unkownn Signal: %d", df.SeqNum)
				panic("Unkownn Signal")
			}
		}

		data := make([]byte, df.Length)
		n, err = conn.Read(data)
		if (err != nil && err != io.EOF) || n != int(df.Length) {
			log.Errorf("Data frame length mismatch, header: %+v", df)
			panic(err)
		}
		df.Data = data[:n]
		log.Debugf("Received data frame from client: %+v", df)
		tw.s2rChannel <- df

		if tw.cancelFlag == -1 || err == io.EOF {
			// reset cancelFlag flag
			tw.cancelFlag = 0
			break
		}
	}
}

func main() {
	// pprof debug
	go func() {
		log.Println(http.ListenAndServe("localhost:6061", nil))
	}()

	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Call stack: %s", debug.Stack())
			log.Fatalf("Runtime error caught: %v, runtime info: %s", r, utils.GoRuntimeInfo())
		}
	}()

	// TODO configuration
	log.SetLevel(log.DebugLevel)
	localAddr := "localhost:8080"
	remoteAddr := "10.64.68.111:80"

	newconnIdChannel := make(chan uint16)

	r2sChannel, s2rChannel := make(chan utils.GotaFrame), make(chan utils.GotaFrame)
	cm := newConnManager(newconnIdChannel, r2sChannel, s2rChannel, remoteAddr)
	//cm := newConnManager(newconnIdChannel, s2rChannel, r2sChannel, remoteAddr)
	go cm.handleConn()

	cancelChannel := make(chan int)
	tm := newTunnelManager(newconnIdChannel, r2sChannel, s2rChannel, cancelChannel, []string{localAddr} /*, remoteAddr*/)
	tm.start()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigs
	fmt.Println(sig)
	os.Exit(0)
}
