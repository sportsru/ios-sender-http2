package main

import (
	"expvar"
	// debug/pprof
	_ "net/http/pprof"

	"fmt"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"

	"github.com/sportsru/ios-sender-http2/config"
)

// WebServer stores global state of web server
type WebServer struct {
	hub *Hub
}

// Run starts tcp listener for http server on addr
// inspired by https://github.com/hoisie/web/blob/master/server.go
func (s *WebServer) Run(addr string) error {
	initHandlers(s)
	expvar.Publish("Goroutines", expvar.Func(func() interface{} {
		return runtime.NumGoroutine()
	}))

	http.Handle("/prom", s.hub.Metrics.getHandler())

	sock, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	go func() {
		fmt.Println("HTTP now available at", addr)
		log.Fatal(http.Serve(sock, nil))
	}()
	return nil
}

func initHandlers(s *WebServer) {
	http.HandleFunc("/tools/nsq_loglevel", s.HandleNSQLogLevel)
	http.HandleFunc("/tools/block_profile_rate", s.HandleBlockProfileRate)
}

func (s *WebServer) HandleNSQLogLevel(w http.ResponseWriter, r *http.Request) {
	level := r.URL.Query().Get("level")
	ll := s.hub.LogLevelLocked
	ll.Lock()
	defer ll.Unlock()

	if len(level) < 1 {
		llNSQ := fmt.Sprintf("NSQ Consumer log level is \"%s\"\n", strings.ToUpper(ll.logLevelNSQ.String()))
		writeResponseWithErr(w, llNSQ)
		return
	}

	// TODO: move to Hub method
	loglevel, err := config.GetNSQLogLevel(strings.ToLower(level))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	ll.logLevelNSQ = loglevel
	s.hub.NsqConsumer.SetLogger(nsqDefaultLogger, loglevel)

	llNSQset := fmt.Sprintf("Set NSQ Consumer log level to \"%s\"\n", strings.ToUpper(level))
	log.Println(llNSQset)
	writeResponseWithErr(w, llNSQset)
}

func (s *WebServer) HandleBlockProfileRate(w http.ResponseWriter, r *http.Request) {
	rateStr := r.URL.Query().Get("rate")

	var info string
	if len(rateStr) < 1 {
		info = `
			SetBlockProfileRate controls the fraction of goroutine blocking events that are reported in the blocking profile.
			The profiler aims to sample an average of one blocking event per rate nanoseconds spent blocked.
			To include every blocking event in the profile, pass rate = 1.
			To turn off profiling entirely, pass rate <= 0.
			`
		writeResponseWithErr(w, info)
		return
	}
	rate, err := strconv.Atoi(rateStr)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if rate <= 0 {
		info = "disable profiling"
	} else if rate == 1 {
		info = "profile everything"
	} else {
		info = fmt.Sprintf("profile with rate %v*tickspersecond / 1*10^9\n", rate)
	}
	log.Println(info)
	runtime.SetBlockProfileRate(rate)
	writeResponseWithErr(w, info)
}

func writeResponseWithErr(w http.ResponseWriter, s string) {
	_, err := w.Write([]byte(s))
	if err != nil {
		log.Println("http err response:", err)
	}
}
