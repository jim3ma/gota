package gota

import (
	"testing"
	"net"
	"time"
	"net/http"
	log "github.com/Sirupsen/logrus"
)

func TestGota_ListenAndServe(t *testing.T) {
	saddr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:32771")

	// Gota Client
	clientConfig := []TunnelActiveConfig{
		{
			LocalAddr:  nil,
			RemoteAddr: saddr,
		},
	}
	client := NewGota(clientConfig)
	caddr := "127.0.0.1:32772"
	go client.ListenAndServe(caddr)

	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()

	time.Sleep(time.Second * 1200)
}

func TestGota_ListenAndServe2(t *testing.T) {
	saddr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:32771")

	// Gota Client
	clientConfig := []TunnelActiveConfig{
		{
			LocalAddr:  nil,
			RemoteAddr: saddr,
		},
	}
	client := NewGota(clientConfig)
	caddr := "127.0.0.1:32770"
	go client.ListenAndServe(caddr)

	go func() {
		log.Println(http.ListenAndServe("localhost:6062", nil))
	}()

	time.Sleep(time.Second * 1200)
}

func TestGota_Serve(t *testing.T) {
	go func(){
		addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:32773")
		if err != nil {
			log.Debug(err)
		}
		l, err := net.ListenTCP("tcp", addr)
		if err != nil {
			log.Debug(err)
		}
		conn, _ :=l.AcceptTCP()
		for {
			data := make([]byte, 65536)
			n, _ := conn.Read(data)
			log.Infof("xxxxx: %s", string(data[:n]))
			WriteNBytes(conn, n, data[:n])
		}
	}()
	saddr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:32771")

	// Gota Server
	serverConfig := TunnelPassiveConfig{
		TCPAddr: saddr,
	}
	server := NewGota(serverConfig)
	raddr := "baidu.com:80"
	//raddr := "127.0.0.1:32773"
	go server.Serve(raddr)

	go func() {
		log.Println(http.ListenAndServe("localhost:6061", nil))
	}()

	time.Sleep(time.Second * 1200)
}
