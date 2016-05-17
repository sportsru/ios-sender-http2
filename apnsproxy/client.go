package apnsproxy

import (
	"bytes"
	"crypto/tls"
	"io/ioutil"
	"net"
	"net/http"
	"time"
	//"log"
	"golang.org/x/net/http2"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/certificate"
	"github.com/sideshow/apns2/payload"
	"gopkg.in/inconshreveable/log15.v2"
)

// GatewayClient mapped on one APNS tcp Gateway connection
type Client struct {
	L log15.Logger

	Topic string
	APNS  *apns2.Client
}

type ClientConfig struct {
	Topic   string
	Sandbox bool

	ConnectionCfg ConnectionConfig
}

type ConnectionConfig struct {
	TLSCert tls.Certificate
	// HttpRequestTimeout sets net/http Client.Timeout
	// «a time limit for requests .. includes connection time, any redirects, and reading the response body»
	// (https://golang.org/pkg/net/http/#Client)
	RequestTimeout time.Duration
	// DialTimeout sets net Dialer.Timeout
	// «maximum amount of time a dial will wait for a connect to complete.»
	// https://golang.org/pkg/net/#Dialer
	ConnectTimeout time.Duration
	// TLSHandshakeTimeout https://golang.org/pkg/net/http/#Transport
	//  «specifies the maximum amount of time waiting to»
	TLSHandshakeTimeout time.Duration
}

func NewNoification(token string) *apns2.Notification {
	return &apns2.Notification{DeviceToken: token}
}

func NewPayload() *payload.Payload {
	return payload.NewPayload()
}

func NewClient(conf *ClientConfig) *Client {
	host := apns2.HostDevelopment
	if !conf.Sandbox {
		host = apns2.HostProduction
	}
	return &Client{
		// Name:  name,
		Topic: conf.Topic,
		APNS: &apns2.Client{
			HTTPClient:  newHttpClient(conf.ConnectionCfg),
			Certificate: conf.ConnectionCfg.TLSCert,
			Host:        host,
		},
	}
}

func setConnectionDefaults(conf ConnectionConfig) ConnectionConfig {
	if int64(conf.RequestTimeout) == 0 {
		conf.RequestTimeout = 5 * time.Second
	}
	if int64(conf.ConnectTimeout) == 0 {
		conf.ConnectTimeout = 5 * time.Second
	}
	if int64(conf.TLSHandshakeTimeout) == 0 {
		conf.TLSHandshakeTimeout = 5 * time.Second
	}
	return conf
}

// newHttpClient tuned version of NewClient
func newHttpClient(conf ConnectionConfig) *http.Client {
	conf = setConnectionDefaults(conf)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{conf.TLSCert},
	}
	if len(conf.TLSCert.Certificate) > 0 {
		tlsConfig.BuildNameToCertificate()
	}
	transport := &http.Transport{
		Dial: (&net.Dialer{
			Timeout: conf.ConnectTimeout,
			//KeepAlive: 24 * time.Hour,
		}).Dial,
		TLSHandshakeTimeout: conf.TLSHandshakeTimeout,
		TLSClientConfig:     tlsConfig,
		//ExpectContinueTimeout: 1 * time.Second,
		//DisableCompression: true,
	}
	if err := http2.ConfigureTransport(transport); err != nil {
		panic(err)
	}

	//log.Println("Init Client{}-+-")
	return &http.Client{
		Transport: transport,
		Timeout:   conf.RequestTimeout,
	}
}

func (c *Client) Send(n *apns2.Notification) (*apns2.Response, error) {
	if n.Topic == "" {
		n.Topic = c.Topic
	}
	return c.APNS.Push(n)
}

func LoadCertAndKey(certFilepath, keyFilepath string) tls.Certificate {
	cert, err := JoinPemFiles(certFilepath, keyFilepath)
	if err != nil {
		panic(err)
	}
	return cert
}

//
// func (c *Client) Log() log15.Logger {
// 	return c.L
// }

func JoinPemFiles(certpath, keypath string) (tls.Certificate, error) {
	bCert, err := ioutil.ReadFile(certpath)
	if err != nil {
		return tls.Certificate{}, err
	}

	buf := bytes.NewBuffer(bCert)
	bKey, err := ioutil.ReadFile(keypath)
	if err != nil {
		return tls.Certificate{}, err
	}
	//buf.WriteString("\n")
	buf.Write(bKey)

	return certificate.FromPemBytes(buf.Bytes(), "")
}
