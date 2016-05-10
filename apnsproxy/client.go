package apnsproxy

import (
	"bytes"
	"crypto/tls"
	"io/ioutil"
	"log"
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
	// Name string
	L log15.Logger

	Topic string
	APNS  *apns2.Client
}

func NewNoification(token string) *apns2.Notification {
	return &apns2.Notification{DeviceToken: token}
}

func NewPayload() *payload.Payload {
	return payload.NewPayload()
}

func NewClient(topic string, cert tls.Certificate, prod bool) *Client {
	host := apns2.DefaultHost
	if prod {
		host = apns2.HostProduction
	}
	return &Client{
		// Name:  name,
		Topic: topic,
		APNS: &apns2.Client{
			HTTPClient:  newHttpClient(cert),
			Certificate: cert,
			Host:        host,
		},
	}
}

// newHttpClient tuned version of NewClient
func newHttpClient(certificate tls.Certificate) *http.Client {
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificate},
	}
	if len(certificate.Certificate) > 0 {
		tlsConfig.BuildNameToCertificate()
	}
	transport := &http.Transport{
		//Proxy: ProxyFromEnvironment,
		Dial: (&net.Dialer{
			Timeout: 5 * time.Second,
			//KeepAlive: 24 * time.Hour,
		}).Dial,
		TLSHandshakeTimeout: 5 * time.Second,
		TLSClientConfig:     tlsConfig,
		//ExpectContinueTimeout: 1 * time.Second,
		//DisableCompression: true,
	}
	if err := http2.ConfigureTransport(transport); err != nil {
		panic(err)
	}
	log.Println("Init Client{}-+-")
	return &http.Client{
		Transport: transport,
		// not sure about it
		Timeout: 5 * time.Second,
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
