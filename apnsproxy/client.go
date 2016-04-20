package apnsproxy

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io/ioutil"
	//"log"
	"sync"

	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/certificate"
	"github.com/sideshow/apns2/payload"
	"gopkg.in/inconshreveable/log15.v2"
)

// TODO: remove!
const PayloadMaxSize = 4096

// GatewayClient mapped on one APNS tcp Gateway connection
type Client struct {
	Name string
	Stat *ClientStat
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

	c.Stat = &ClientStat{}
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

// ClientStat stores GatewayClient on wire statistics
type ClientStat struct {
	Connected  bool
	Reconnects int64
	LastError  string
	Drop       int64
	Send       int64
	Skip       int64
	mu         sync.RWMutex
}

// String concurrent safe implementation of ClientStat serialization to json
func (v *ClientStat) String() string {
	v.mu.RLock()
	defer v.mu.RUnlock()

	j, _ := json.Marshal(v)
	return string(j)
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
