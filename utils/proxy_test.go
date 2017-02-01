package utils

import (
	"net/url"
	"testing"

	"golang.org/x/net/proxy"
	"os"
)

func TestHttpProxy(t *testing.T){
	httpProxyEnv := os.Getenv("http_proxy")
	httpProxyURI, _ := url.Parse(httpProxyEnv)
	httpDialer, err := proxy.FromURL(httpProxyURI, Direct)

	conn, err := httpDialer.Dial("tcp", "google.com:80")
	if err != nil {
		t.Errorf("Create http tunnel error", err)
	}
	t.Logf("Create http tunnel OK: %+v\n", conn)
}

func TestHttpsProxy(t *testing.T){
	httpsProxyEnv := os.Getenv("https_proxy")
	httpsProxyURI, _ := url.Parse(httpsProxyEnv)
	httpsDialer, err := proxy.FromURL(httpsProxyURI, HttpsDialer)

	conn, err := httpsDialer.Dial("tcp", "google.com:443")
	if err != nil {
		t.Errorf("Create https tunnel error", err)
	}
	t.Logf("Create https tunnel OK: %+v\n", conn)
}
