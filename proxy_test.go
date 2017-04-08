package gota

import (
	"net/url"
	"testing"

	"golang.org/x/net/proxy"
	"os"
)

func TestHttpProxy(t *testing.T) {
	httpProxyEnv := os.Getenv("http_proxy")
	if httpProxyEnv == "" {
		t.Log("Error http_proxy environment variable.")
		return
	}

	httpProxyURI, _ := url.Parse(httpProxyEnv)
	httpDialer, _ := proxy.FromURL(httpProxyURI, Direct)

	conn, err := httpDialer.Dial("tcp", "google.com:80")
	if err != nil {
		t.Errorf("Create http tunnel error: %+v", err)
		return
	}
	t.Logf("Create http tunnel OK: %+v\n", conn)
	conn.Close()
}

func TestHttpsProxy(t *testing.T) {
	httpsProxyEnv := os.Getenv("https_proxy")
	if httpsProxyEnv == "" {
		t.Log("Error https_proxy environment variable.")
		return
	}
	httpsProxyURI, _ := url.Parse(httpsProxyEnv)
	httpsDialer, _ := proxy.FromURL(httpsProxyURI, HTTPSDialer)

	conn, err := httpsDialer.Dial("tcp", "google.com:443")
	if err != nil {
		t.Errorf("Create https tunnel error: %+v", err)
		return
	}
	t.Logf("Create https tunnel OK: %+v\n", conn)
	conn.Close()
}
