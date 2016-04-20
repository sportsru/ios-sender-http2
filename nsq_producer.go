package main

// inspired by https://github.com/nsqio/nsq/blob/master/apps/nsq_to_nsq/nsq_to_nsq.go
import (
	"log"

	"gopkg.in/inconshreveable/log15.v2"

	"github.com/bitly/go-hostpool"
	nsq "github.com/nsqio/go-nsq"
)

type PublishHandler struct {
	counter   uint64
	addresses []string

	producers map[string]*nsq.Producer
	hostPool  hostpool.HostPool
}

func NewPublishHandler(conf *nsq.Config, destNsqdTCPAddrs []string) *PublishHandler {
	producers := make(map[string]*nsq.Producer)
	for _, addr := range destNsqdTCPAddrs {
		producer, err := nsq.NewProducer(addr, conf)
		if err != nil {
			log.Fatalf("failed creating producer %s", err)
		}
		producers[addr] = producer
	}

	hostPool := hostpool.New(destNsqdTCPAddrs)
	// 	hostPool = hostpool.NewEpsilonGreedy(destNsqdTCPAddrs, 0, &hostpool.LinearEpsilonValueCalculator{})

	handler := &PublishHandler{
		addresses: destNsqdTCPAddrs,
		producers: producers,
		hostPool:  hostPool,
	}
	return handler
}

type logger struct {
	l15 log15.Logger
}

// FIXME: !!! set proper log level !!!
func (l logger) Output(calldepth int, s string) error {
	l.l15.Info(s)
	return nil
}

func (ph *PublishHandler) HandleMessage(topic string, body []byte) error {
	hostPoolResponse := ph.hostPool.Get()
	p := ph.producers[hostPoolResponse.Host()]
	err := p.Publish(topic, body)
	if err != nil {
		hostPoolResponse.Mark(err)
		return err
	}
	hostPoolResponse.Mark(nil)
	return nil
}

// FIXME: !!!
func (ph *PublishHandler) SetNSQLogger(l logger, lvl nsq.LogLevel) {
	for _, p := range ph.producers {
		p.SetLogger(l, lvl)
	}
}

func (ph *PublishHandler) Stop() {
	for _, p := range ph.producers {
		p.Stop()
	}
}
