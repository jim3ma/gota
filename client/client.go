package main

import (
	//"bytes"
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

	humanize "github.com/dustin/go-humanize"
	log "github.com/Sirupsen/logrus"
)

type ConnManager struct {
	//mutex            *sync.Mutex
	nextConnID       uint16
	connChannel      <-chan *net.TCPConn
	connCloseChannel chan uint16
	x2cChannel       chan<- utils.GotaFrame
	c2xChannel       <-chan utils.GotaFrame
	connectionPool   map[uint16]*ConnHandler
}

func newConnManager(in <-chan *net.TCPConn, x2cChannel chan utils.GotaFrame, c2xChannel chan utils.GotaFrame) *ConnManager {
	closeChannel := make(chan uint16)
	connPool := make(map[uint16]*ConnHandler)
	c := &ConnManager{
		nextConnID:       1,
		connChannel:      in,
		connCloseChannel: closeChannel,
		x2cChannel:       x2cChannel,
		c2xChannel:       c2xChannel,
		connectionPool:   connPool,
	}
	//go c.handleConn()
	return c
}

// s2c connection for local port, and dispatch a Connection Handler to forward traffic
func (c *ConnManager) handleConn() {
	go c.closeConn()
	go c.dispatch()
	for conn := range c.connChannel {
		//t.mutex.Lock()
		c2xChannel := make(chan utils.GotaFrame)
		ch := ConnHandler{
			cid:              c.nextConnID,
			conn:             conn,
			connCloseChannel: c.connCloseChannel,
			x2cChannel:       c.x2cChannel,
			c2xChannel:       c2xChannel,
			readClosed: false,
			writeClosed: false,
		}
		// all handlers share one c2s channel, and every handler uses one s2c channel,
		// we need register the s2c channel, so we can forward traffic from tunnels to local connection
		c.connectionPool[c.nextConnID] = &ch
		go ch.start()
		if c.nextConnID == utils.MaxConnID {
			c.nextConnID = 1
		} else {
			c.nextConnID += 1
		}
		//t.mutex.Unlock()
	}
}

// c2s Magic number to server, than server will create a new connection
//func (c *ConnManager) createConnOnServer(cid uint16){
//	c.x2cChannel <-
//}

// s2c from c2xChannel and forward to special s2c channel according the connection ID
func (c *ConnManager) dispatch() {
	for d := range c.c2xChannel {
		log.Debugf("Received data from tunnel: %s", d)
		if ch, ok := c.connectionPool[d.ConnId]; ok {
			ch.c2xChannel <- d
		} else {
			log.Errorf("Connection didn't exist, connection id: %d", d.ConnId)
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
	x2cChannel       chan<- utils.GotaFrame
	c2xChannel       chan utils.GotaFrame
	writeClosed      bool
	readClosed       bool
}

func (ch *ConnHandler) start() {
	// c2s Magic number to server, than server will create a peer connection
	req := utils.GotaFrame{
		ConnId: ch.cid,
		Length: 0,
		SeqNum: uint32(utils.TMCreateConnSeq),
	}
	log.Debugf("Try to create a peer connection on server, connection id: %d", ch.cid)
	ch.x2cChannel <- req

	// wait for server response
	res := <-ch.c2xChannel
	if res.SeqNum != utils.TMCreateConnOKSeq {
		log.Error("Create a peer connection failed, close client connection")
		//ch.connCloseChannel <- ch.cid
	}
	log.Debugf("Created a peer connection on server, connection id: %d", ch.cid)
	go ch.x2c()
	go ch.c2x()
}

func (ch *ConnHandler) x2c() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Read data failed: %v", r)
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
			df := utils.GotaFrame{
				ConnId: ch.cid,
				SeqNum: seq,
				Length: uint16(n),
				Data:   data[:n],
			}
			seq += 1
			ch.x2cChannel <- df
		} else {
			log.Warn("Received empty data from x")
		}
		if err == io.EOF {
			log.Debugf("Received io.EOF from x(%v), start to close write connection on server", ch.conn.RemoteAddr())

			// close write connection between server and client
			ch.x2cChannel <- utils.GotaFrame{
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

func (ch *ConnHandler) c2x() {
	defer func() {
		if r := recover(); r != nil {
			log.Error("Write data failed: %s", r)
			log.Errorf("Call stack: %s", debug.Stack())
		}
	}()

	var seq uint32
	seq = 1
	cache := make(map[uint32][]byte)
	for d := range ch.c2xChannel {
		if d.Length == utils.CtrlFrameLength {
			if d.SeqNum == utils.TMCloseConnSeq {
				log.Debugf("Received close write connection signal, try to close write connection")
				ch.conn.CloseWrite()
				return
			}
		}
		if d.SeqNum == seq {
			log.Debugf("Received wanted data frame seq from tunnel: %d", seq)
			//_, err := ch.conn.Write(d.Data)
			err := utils.WriteNBytes(ch.conn, int(d.Length), d.Data)
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
			// check cache and send to client
			for {
				if data, ok := cache[seq]; ok {
					//_, err := ch.conn.Write(data)
					err := utils.WriteNBytes(ch.conn, len(data), data)
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
			// cache for disorder data frame
			log.Debugf("Received data frame seq from tunnel: %d, but want to receive data frame seq: %d, cache it",
				d.SeqNum, seq)
			cache[d.SeqNum] = d.Data
		} else {
			log.Warnf("Received data frame seq from tunnel: %d, but the data frame already send to x, dropped", d.SeqNum)
		}
	}
}

// connection pool
type TunnelManager struct {
	// Send Woker Pool
	//SWokerPool chan *TunnelWorker
	// Receive Worker pool
	//RWokerPool chan *TunnelWorker
	x2cChannel    <-chan utils.GotaFrame
	c2xChannel    chan<- utils.GotaFrame
	cancelChannel chan int
	localIPs      []string
	remoteAddrs   []string
	workerPool    []*TunnelWorker
	mode          int
}

func newTunnelManager(x2c chan utils.GotaFrame, c2x chan utils.GotaFrame, cancel chan int, mode int, localIPs []string, remoteAddrs []string) *TunnelManager {
	t := &TunnelManager{
		x2cChannel:    x2c,
		c2xChannel:    c2x,
		cancelChannel: cancel,
		localIPs:      localIPs,
		remoteAddrs:   remoteAddrs,
		mode:          mode,
	}
	//go t.start()
	return t
}

func (t *TunnelManager) start() {
	// TODO multi mode support
	switch t.mode {
	case utils.TMConnBiuniqueMode:
		log.Info("Work Mode: Biunique")
	case utils.TMConnOverlapMode:
		log.Info("Work Mode: Overlap")
	case utils.TMConnMultiBiuniqueMode:
		log.Info("Work Mode: Multi Biunique")
	case utils.TMConnMultiOverlapMode:
		log.Info("Work Mode: Multi Overlap")
	default:
		log.Error("Unknown Worker Mode")
		panic("Unknown Worker Mode")
	}

	for _, lAddr := range t.localIPs {
		for _, rAddr := range t.remoteAddrs {
			log.Infof("Local IP address: %s, remote address: %s", lAddr, rAddr)
			heartbeat := make(chan int)
			tw := &TunnelWorker{
				localAddr:     lAddr,
				remoteAddr:    rAddr,
				cancelled:     false,
				cancelChannel: t.cancelChannel,
				heartbeatChan: heartbeat,
				x2cChannel:    t.x2cChannel,
				c2xChannel:    t.c2xChannel,
			}
			go tw.start()
		}
	}
}

type TunnelWorker struct {
	localAddr     string
	remoteAddr    string
	cancelled     bool
	cancelChannel chan int
	heartbeatChan chan int
	// read from client and c2s to server
	x2cChannel <-chan utils.GotaFrame
	// s2c from server and c2s to client
	c2xChannel chan<- utils.GotaFrame
	stat utils.Statistic
}

func (tw *TunnelWorker) heartbeat(stop <-chan int) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Heartbeat error: %s", r)
			log.Errorf("Call stack: %s", debug.Stack())
			go tw.heartbeat(stop)
		}
	}()
	for {
		select {
		case <-time.After(time.Second * utils.TMHeartBeatSecond):
			tw.heartbeatChan <- 0
			if tw.cancelled == true {
				log.Info("Tunnel work was canneled")
				break
			}
		case <-time.After(time.Second * utils.TMStatReportSecond):
			log.Infof("Traffic report for client(%s) & server(%s): { sent: %s bytes, %s/second, received: %s bytes, %s/second }",
				tw.localAddr, tw.remoteAddr,
				humanize.Comma(tw.stat.SentBytes), humanize.Comma(tw.stat.SendSpeed()),
				humanize.Comma(tw.stat.ReceivedBytes), humanize.Comma(tw.stat.ReceiveSpeed()))
		case <- stop:
			log.Info("Reveived stop hearbeat signal, stopped")
		}
	}
}

//TODO retry when can't create tunnel to server
func (tw *TunnelWorker) start() {
	tw.stat = utils.Statistic{
		SentBytes: 0,
		ReceivedBytes: 0,
		StartSeconds: time.Now().Unix(),
	}
	// connect server
	lAddr, err := net.ResolveTCPAddr("tcp", tw.localAddr+":0")
	if err != nil {
		log.Errorf("Using Local Address: %s, Error: %s", lAddr, err)
		return
	}
	rAddr, err := net.ResolveTCPAddr("tcp", tw.remoteAddr)
	if err != nil {
		log.Errorf("Using Remote Address: %s, Error: %s", rAddr, err)
		return
	}

	conn, err := net.DialTCP("tcp", lAddr, rAddr)
	if err != nil {
		log.Errorf("Connect to Server: %s, using Local Address: %s, Error: %s", rAddr, lAddr, err)
		return
	}
	defer conn.Close()

	// update locate and remote address
	tw.localAddr = conn.LocalAddr().String()
	tw.remoteAddr = conn.RemoteAddr().String()

	log.Debugf("Created a tunnel: %v to server: %v", conn.LocalAddr(), conn.RemoteAddr())

	done, stopHeartbeat := make(chan int), make(chan int)
	go tw.heartbeat(stopHeartbeat)
	go tw.c2s(done, conn)
	go tw.s2c(done, conn)
	log.Infof("TunnelWorker start to forward traffic, local address: %s, remote address: %s", tw.localAddr, tw.remoteAddr)
	<-done
	<-done
	stopHeartbeat <- 0
}

func (tw *TunnelWorker) restart() {
	tw.cancelled = false
	go tw.start()
}

// traffic -> gota client -> internet -> gota server -> ...
func (tw *TunnelWorker) c2s(done chan<- int, conn *net.TCPConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Runtime error caught: %v, runtime info: %s", r, utils.GoRuntimeInfo())
			log.Errorf("Call stack: %s", debug.Stack())
		}
		tw.cancelled = true
		done <- 0
	}()

	log.Debugf("Tunnel work start to forward data from x client(%v) to server(%v)", conn.LocalAddr(), conn.RemoteAddr())
	for {
		select {
		case d := <-tw.x2cChannel:
			err := utils.WriteNBytes(conn, int(d.Length) + 8, utils.WrapDataFrame(d))
			if err != nil {
				panic(err)
			}
			tw.stat.AddSentBytes(int64(d.Length))
			log.Debugf("Wrote %d bytes", d.Length)
			log.Debugf("Received data frame from x, send to server, data: %s", d)
		case <-tw.cancelChannel:
			log.Infof("Shutdown Worker: %v", tw)
			_, err := conn.Write(utils.TMCloseTunnelBytes)
			if err != nil {
				log.Fatalf("Send Close Signal failed duo to: %s", err)
			}
			return
		case <-tw.heartbeatChan:
			_, err := conn.Write(utils.TMHeartBeatBytes)
			log.Debugf("Sent heartbeat to server(%s) from client(%s)", conn.RemoteAddr(), conn.LocalAddr())
			if err != nil {
				log.Errorf("HeartBeat failed duo to: %s, stop this worker", err)
				return
			}
		}
	}
}

// traffic <- gota client <- internet <- gota server <- ...
func (tw *TunnelWorker) s2c(done chan<- int, conn *net.TCPConn) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Runtime error caught: %v, runtime info: %s", r, utils.GoRuntimeInfo())
			log.Errorf("Call stack: %s", debug.Stack())
		}
		done <- 0
	}()
	log.Debugf("Tunnel work start to forward data from server(%v) to x client(%v)",
		conn.RemoteAddr(), conn.LocalAddr())
	for {
		header := make([]byte, 8)
		n, err := conn.Read(header)
		if n == 0 && err == nil {
			log.Debug("Receive empth data, skip and continue")
			continue
		} else if err != io.EOF && (err != nil || n != 8) {
			// TODO
			log.Error("Received data frame header error")
			//panic(err)
		}
		if err == io.EOF {
			log.Infof("Received data frame header io.EOF, stop this worker(client: %v, server: %v)",
				conn.LocalAddr(), conn.RemoteAddr())
			break
		}
		df := utils.UnwrapDataFrame(header)
		log.Debugf("Received data frame header from server: %s", df)

		if df.Length == 0 {
			switch df.SeqNum {
			case utils.TMHeartBeatSeq:
				log.Debugf("Received heartbeat signal from server(%s) to client(%s)", conn.RemoteAddr(), conn.LocalAddr())
				continue
			//case utils.TMCreateConnSeq:
			//	log.Debug("Received create connection signal")
			//	log.Error("Create connection signal only used in server")
			//	continue
			case utils.TMCreateConnOKSeq:
				log.Debugf("Received create connection ok signal, connection id: %d", df.ConnId)
				tw.c2xChannel <- df
				continue
			case utils.TMCloseConnSeq:
				log.Debug("Received close connection signal")
				tw.c2xChannel <- df
				continue
			//case utils.TMCloseTunnelSeq:
			//	log.Info("Receive close tunnel signal")
				// TODO close tunnel
			default:
				log.Errorf("Unkownn signal: %d", df.SeqNum)
				panic("Unkownn signal")
			}
		}

		df.Data, err = utils.ReadNBytes(conn, int(df.Length))
		if err != nil {
			panic(err)
		}

		tw.stat.AddReceivedBytes(int64(n))
		tw.c2xChannel <- df

		if tw.cancelled == true || err == io.EOF {
			break
		}
	}
}

func main() {
	// pprof debug
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Call stack: %s", debug.Stack())
			log.Fatalf("Runtime error caught: %v, runtime info: %s", r, utils.GoRuntimeInfo())
		}
	}()

	// TODO configuration
	log.SetLevel(log.DebugLevel)
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

	x2cChannel, c2xChannel := make(chan utils.GotaFrame), make(chan utils.GotaFrame)
	cm := newConnManager(newConnChannel, x2cChannel, c2xChannel)
	go cm.handleConn()

	cancelChannel := make(chan int)
	//tm := newTunnelManager(x2cChannel, c2xChannel, cancelChannel, TMConnBiuniqueMode, []string{"127.0.0.1", "127.0.0.1"}, []string{"127.0.0.1:8080", "127.0.0.1:8080"})
	tm := newTunnelManager(x2cChannel, c2xChannel, cancelChannel, utils.TMConnBiuniqueMode,
		[]string{ "192.168.91.128", "192.168.91.128", "192.168.91.128", "192.168.91.128"},
		[]string{"10.202.240.252:8080", "10.202.240.252:8080", "10.202.240.252:8080", "10.202.240.252:8080"})
	//tm := newTunnelManager(x2cChannel, c2xChannel, cancelChannel, utils.TMConnBiuniqueMode, []string{"192.168.91.128"}, []string{"192.168.91.128:8080"})
	go tm.start()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigs
		fmt.Println(sig)
		os.Exit(1)
	}()

	for {
		conn, err := listener.AcceptTCP()
		if err != nil {
			//panic(err)
			// TODO error handle which accept new connection
			log.Errorf("Accept Connection Error: %s", err)
			continue
		}
		log.Debugf("Received new connection from %s", conn.RemoteAddr())
		newConnChannel <- conn
	}
}
