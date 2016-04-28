package apnsproxy

import (
	"bytes"
	"crypto/tls"
	"io/ioutil"
	//"log"
	"sync"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/certificate"
	"github.com/sideshow/apns2/payload"
	"gopkg.in/inconshreveable/log15.v2"
)

// GatewayClient mapped on one APNS tcp Gateway connection
type Client struct {
	Name string
	L    log15.Logger

	topic  string
	client *apns2.Client

	sync.Mutex
}

func NewNoification(token string) *apns2.Notification {
	return &apns2.Notification{DeviceToken: token}
}

func NewPayload() *payload.Payload {
	return payload.NewPayload()
}

func NewClient(name, topic string) *Client {
	c := new(Client)
	c.Name = name
	c.topic = topic

	return c
}

func (c *Client) Send(n *apns2.Notification) (*apns2.Response, error) {
	if n.Topic == "" {
		n.Topic = c.topic
	}
	return c.client.Push(n)
}

func (c *Client) LoadCertAndKey(certFilepath, keyFilepath string) error {
	cert, err := JoinPemFiles(certFilepath, keyFilepath)
	if err != nil {
		return err
	}

	c.client = apns2.NewClient(cert)
	_ = c.client.Production()
	return nil
}

//
func (c *Client) Log() log15.Logger {
	return c.L
}

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
