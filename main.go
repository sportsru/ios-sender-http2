package main

import (
	"errors"
	"expvar"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sportsru/ios-sender-http2/apnsproxy"
	"github.com/sportsru/ios-sender-http2/config"

	"github.com/BurntSushi/toml"
	nsq "github.com/nsqio/go-nsq"
	"gopkg.in/inconshreveable/log15.v2"
)

var hubStat = expvar.NewMap("APNSconections")

// VERSION application version
// (should be redefined at build phase)
var VERSION = "1.0-UnknownBuild"

// NsqLogLevelLocked boxing together mutex and current log level value
type NsqLogLevelLocked struct {
	logLevel    string
	logLevel15  log15.Lvl
	logLevelNSQ nsq.LogLevel
	sync.Mutex
}

// Hub main App struct
// implements nsq.Handler interface
// stores all global structures
type Hub struct {
	// all connections to APNS (per application bundle)
	Consumers map[string]*apnsproxy.Client
	// interface for consume messages from NSQ and produce it further
	// FIXME: rename Producer to smth more clean & precise (apnsProducer?)
	//Producer MessageProducer

	//NsqSrc *NSQSource
	// Nsq fields
	NsqFeedbackProducer *PublishHandler
	// nsq consumer object
	NsqConsumer    *nsq.Consumer //*nsq.Consumer
	LogLevelLocked *NsqLogLevelLocked

	// global hub statistics
	MessagesStat *HubMessagesStat
	// config from toml file
	Config *config.TomlConfig

	// TTL in seconds
	ProducerTTL int64
	ConsumerTTL int64

	L      log15.Logger
	logctx logcontext

	sync.RWMutex
}

// XXX: check logLvl necessarity
type logcontext struct {
	hostname string
	handler  log15.Handler
}

// HubMessagesStat global messages stat
type HubMessagesStat struct {
	total      int64
	totalUniq  int64
	resend     int64
	resendUniq int64
	drop       int64
	skip       int64
	send       int64
	sendUniq   int64
}

var (
	configPath = flag.String("config", "config.toml", "config file")
	silent     = flag.Bool("silent", false, "no verbose output")
	debug      = flag.Bool("debug", false, "debug mode (very verbose output)")
	notSend    = flag.Bool("null", false, "don't send messages (/dev/null mode)")
	//
	log15Level  = flag.String("log-level", "info", "default log level (overwrites by -debug & -verbose flags)")
	jsonLog     = flag.Bool("json-log", false, "use JSON for logging")
	httpAddr    = flag.String("http-stat", ":9090", "stat's http addr")
	showVersion = flag.Bool("version", false, "show version")

	// TODO: make it useful again
	onlyTestAPNS = flag.Bool("test-only", false, "test APNS connections and exit")
)

var GlobalLog = log15.New()

func main() {
	var err error
	hostname, err := os.Hostname()
	if err != nil {
		LogAndDieShort(GlobalLog, err)
	}

	flag.Parse()
	if *showVersion {
		fmt.Println(VERSION)
		os.Exit(0)
	}

	logLvl, err := log15.LvlFromString(*log15Level)
	if err != nil {
		LogAndDieShort(GlobalLog, err)
	}
	if *debug {
		logLvl = log15.LvlDebug
	} else if *silent {
		logLvl = log15.LvlError
	}

	// logging setup
	GlobalLog = log15.New("host", hostname)
	basehandler := log15.StdoutHandler
	if *jsonLog {
		basehandler = log15.StreamHandler(os.Stdout, log15.JsonFormat())
	}
	loghandler := log15.LvlFilterHandler(logLvl, basehandler)
	GlobalLog.SetHandler(loghandler)
	GlobalLog.Warn("effective logLevel", "level", logLvl.String())

	// configure
	var cfg config.TomlConfig
	if _, err = toml.DecodeFile(*configPath, &cfg); err != nil {
		LogAndDieShort(GlobalLog, err)
	}
	if *debug {
		fmt.Fprintf(os.Stderr, "Config => %+v\n", cfg)
	}

	// create & configure hub
	hub := &Hub{
		logctx: logcontext{
			hostname: hostname,
			handler:  loghandler,
		},
		L:              GlobalLog,
		LogLevelLocked: &NsqLogLevelLocked{logLevel15: logLvl},
	}

	hub.InitWithConfig(cfg)

	// run hub
	end := make(chan struct{})
	go func() {
		hub.Run()
		end <- struct{}{}
	}()

	// run webserver (expvar & control)
	server := &WebServer{hub: hub}
	err = server.Run(*httpAddr)
	if err != nil {
		LogAndDieShort(GlobalLog, err)
	}
	<-end
	GlobalLog.Info("Bye!")
}

// InitHubWithConfig create *Hub struct based on config and default values
func (h *Hub) InitWithConfig(cfg config.TomlConfig) {
	var (
		err       error
		errorsCnt int
	)

	connections := make(map[string]*apnsproxy.Client)
	for nick, appCfg := range cfg.APNSapp {
		_ = nick
		client := apnsproxy.NewClient(appCfg.Name, appCfg.Name)
		err = client.LoadCertAndKey(appCfg.KeyOpen, appCfg.KeyPrivate)
		clientLog := log15.New("host", h.logctx.hostname, "app", appCfg.Name)
		clientLog.SetHandler(h.logctx.handler)

		client.L = clientLog

		connections[appCfg.Name] = client
		//TODO: err = testAPNS(client)
		hubStat.Set(appCfg.Name, client.Stat)
		if err != nil {
			h.L.Error("APNS client test failed"+err.Error(), "app", appCfg.Name)
			errorsCnt++
			continue
		}
		clientLog.Info("connection OK")
	}
	if *onlyTestAPNS {
		os.Exit(errorsCnt)
	}

	if len(cfg.NSQ.LogLevel) < 1 {
		cfg.NSQ.LogLevel = "info" // FIXME: move to const
	}
	h.LogLevelLocked.logLevelNSQ, err = config.GetNSQLogLevel(cfg.NSQ.LogLevel)
	if err != nil {
		LogAndDieShort(h.L, err)
	}

	h.Consumers = connections
	h.MessagesStat = &HubMessagesStat{}

	h.Config = &cfg
	h.ProducerTTL = parseTTLtoSeconds(cfg.NSQ.TTL)
	h.ConsumerTTL = parseTTLtoSeconds(cfg.APNS.TTL)
}

func parseTTLtoSeconds(s string) int64 {
	dur, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return int64(dur.Seconds())
}

// Run messages routing
// 1. call RunWithHandler on Producer
// 2. handle system interraptions and NSQ stop channel
func (h *Hub) Run() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	h.CreateFeedbackProducer()
	h.ActivateMessageProducers()

	for {
		select {
		case <-h.NsqConsumer.StopChan:
			h.L.Info("hub stopped", "stat:", hubStat.String())
			return
		case sig := <-sigChan:
			h.L.Debug("Got stop signal", "num:", sig.String())
			_ = sig
			h.Stop()
		}
	}
}

func (h *Hub) Stop() {
	h.L.Debug("nsqConsumer stopping")
	h.NsqConsumer.Stop()
	if h.NsqFeedbackProducer != nil {
		h.L.Debug("NsqFeedbackProducer stopping")
		h.NsqFeedbackProducer.Stop()
	}
}

// TODO: add -t flag for testing only
func testAPNS(client *apnsproxy.Client) (err error) {
	return nil
}

// TODO: use log15?
var nsqDefaultLogger = log.New(os.Stderr, "", log.Flags())

func (h *Hub) SendFeedback(token string, ctx []interface{}) {
	if h.NsqFeedbackProducer == nil {
		h.L.Info("NsqFeedbackProducer not set. Skip", "token", token)
		return
	}
	//ctx = append(ctx, []interface{}{"token", token}...)
	json := `{ "platform": "APNS", "token_list": ["` + token + `"] }`
	err := h.NsqFeedbackProducer.HandleMessage(h.Config.NSQ.FeedbackTopic, []byte(json))
	if err != nil {
		h.L.Error("send on feedback topic failed", ctx...)
		return
	}
	h.L.Info("send on feedback ok", ctx...)
}

func (h *Hub) CreateFeedbackProducer() {
	nsqdAddrs := h.Config.NSQ.NsqdAddrs
	if len(nsqdAddrs) < 1 {
		return
	}
	if h.Config.NSQ.FeedbackTopic == "" {
		return
	}

	cfg := nsq.NewConfig()
	cfg.UserAgent = fmt.Sprintf("mpush-apns-agent/%s", VERSION)
	h.NsqFeedbackProducer = NewPublishHandler(cfg, nsqdAddrs)
	return
}

func (h *Hub) ActivateMessageProducers() {
	//ns := h.Produce
	hcfgNsq := h.Config.NSQ
	concurrency := hcfgNsq.Concurrency
	if concurrency <= 0 {
		concurrency = 100 // FIXME: move to const
	}

	cfg := nsq.NewConfig()
	cfg.UserAgent = fmt.Sprintf("mpush-apns-agent/%s", VERSION)
	cfg.DefaultRequeueDelay = time.Second * 5
	if hcfgNsq.MaxInFlight > 0 {
		cfg.MaxInFlight = hcfgNsq.MaxInFlight
	}

	consumer, err := nsq.NewConsumer(hcfgNsq.Topic, hcfgNsq.Channel, cfg)
	if err != nil {
		LogAndDie(h.L, "NSQ consumer creation error", err, []interface{}{})
	}

	consumer.SetLogger(nsqDefaultLogger, h.LogLevelLocked.logLevelNSQ)

	h.L.Debug("set Nsq consumer concurrency", "n", concurrency)
	consumer.AddConcurrentHandlers(nsq.Handler(h), concurrency)
	h.L.Debug("Add *hub to NSQ handlers", "consumer_config", fmt.Sprintf("%+v", cfg))

	var addrs []string
	if hcfgNsq.LookupAddrs != nil {
		addrs = hcfgNsq.LookupAddrs
		err = consumer.ConnectToNSQLookupds(addrs)
	} else if hcfgNsq.NsqdAddrs != nil {
		addrs = hcfgNsq.NsqdAddrs
		err = consumer.ConnectToNSQDs(addrs)
	} else {
		LogAndDieShort(h.L, errors.New("You should set at least one nsqd or nsqlookupd address"))
	}

	logCtx := []interface{}{"addrs", addrs}
	if err != nil {
		LogAndDie(h.L, "NSQ connection error", err, logCtx)
	}

	h.NsqConsumer = consumer
	h.L.Debug("NSQ connected to", logCtx...)
}

func LogAndDieShort(l log15.Logger, err error) {
	l.Error(err.Error())
	panic(err)
}

func LogAndDie(l log15.Logger, msg string, err error, args []interface{}) {
	l.Error(msg+err.Error(), args...)
	panic(err)
}
