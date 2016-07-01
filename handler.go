package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync/atomic"
	"time"

	"github.com/nsqio/go-nsq"
	"github.com/sideshow/apns2"
	"github.com/sportsru/ios-sender-http2/apnsproxy"
)

// NSQpush json struct of awaited data from NSQ topic
type NSQpush struct {
	Extra       extraField   `json:"extra"`
	PayloadAPNS payloadField `json:"payload_apns"`
	AppInfo     appInfoField `json:"nsq_to_nsq"`
	Message     string       `json:"notification_message"`
	DbRes       dbResField   `json:"db_res"`
}

type appInfoField struct {
	Topic         string
	AppBundleName string `json:"app_bundle_id"`
	Token         string
	Sandbox       bool
}

type dbResField struct {
	Timestamp int64
}

type payloadField struct {
	Sound string
	Badge int
}

type extraField struct {
	EventID int32 `json:"event_id"`
	SportID int32 `json:"sport_id"`
	Type    string
	TTL     int32 `json:"ttl"`
	EventTs int32 `json:"event_timestamp"`
}

type payloadExtraField struct {
	EventID  int32  `json:"event_id"`
	SportID  int32  `json:"sport_id"`
	FlagType string `json:"flagType"`
	Version  int32  `json:"v"`
}

func (h *Hub) HandleMessage(m *nsq.Message) error {
	var err error
	start := time.Now()
	res, err := h.sendMessage(m)
	if err != nil {
		if ctxErr, ok := err.(ErrorWithContext); ok {
			h.L.Error(ctxErr.Msg, ctxErr.Ctx...)
		} else {
			h.L.Error(err.Error())
		}
	}
	if res != nil {
		ts := time.Since(start)
		h.Metrics.Update(ts, res.App, res.State)
		h.L.Info("time", []interface{}{
			"app", res.App,
			"id", res.Cnt,
			"time", fmt.Sprintf("%v", float64(ts)/float64(time.Millisecond)),
		}...)
	}
	//m.Finish()
	return nil
}

type PushResult struct {
	Cnt   int64
	App   string
	State string
}

func (h *Hub) sendMessage(m *nsq.Message) (*PushResult, error) {
	var err error
	// TODO: if *debug {
	// h.L.Debug("handle NSQ message", "counter", counter)
	// defer h.L.Debug("finished NSQ message", "counter", counter)
	// }
	m.DisableAutoResponse()

	// http://blog.golang.org/json-and-go
	j := &NSQpush{}
	err = json.Unmarshal(m.Body, &j)
	if err != nil {
		//h.L.Error("failed to parse JSON: "+err.Error(), "body", string(m.Body))
		return nil, ErrorWithContext{
			Msg: "failed to parse JSON: " + err.Error(),
			Ctx: []interface{}{"body", string(m.Body)},
		}
	}

	counter := atomic.AddInt64(&h.PushCounter, 1)
	name := j.AppInfo.AppBundleName
	pushRes := &PushResult{
		App: name,
		Cnt: counter,
	}
	// TODO: add support of
	if j.AppInfo.Sandbox {
		name = name + "-dev"
	}

	// Check Application name
	conn, ok := h.Consumers[name]
	if !ok {
		m.Finish()
		h.L.Debug("appname not found, skip message", "appname", name)
		// TODO: move states ti Consts
		pushRes.State = "app_not_found"
		return pushRes, ErrorWithContext{
			Msg: "application name not found",
			Ctx: []interface{}{"name", name},
		}
	}

	// Prepare logging context
	msgCtx := []interface{}{
		"counter", counter,
		// "app", name,
		"token", j.AppInfo.Token,
	}
	conn.L.Debug("start process message", msgCtx...)

	// Check TTL
	nowUnix := time.Now().UTC().Unix()
	jTs := int64(j.Extra.EventTs + j.Extra.TTL)
	delta := (jTs + h.ProducerTTL) - nowUnix
	if delta <= 0 {
		m.Finish()

		pushRes.State = "ttl_is_over"
		msgTTLCtx := append(msgCtx, []interface{}{
			"TTL", h.ProducerTTL,
			"timestamp", jTs,
			"delta", delta,
			"body", string(m.Body),
		}...)
		conn.L.Warn("message TTL is over", msgTTLCtx...)
		return pushRes, nil
	}

	// create Notify object
	extra := &payloadExtraField{
		EventID:  j.Extra.EventID,
		FlagType: j.Extra.Type,
		SportID:  j.Extra.SportID,
		Version:  0,
	}
	notification := apnsproxy.NewNoification(j.AppInfo.Token)
	payload := apnsproxy.NewPayload().Alert(j.Message).Badge(j.PayloadAPNS.Badge).Sound(j.PayloadAPNS.Sound)
	notification.Payload = payload.Custom("extra", extra)
	notification.Expiration = time.Unix(jTs+h.ConsumerTTL, 0)

	payJSON, _ := notification.MarshalJSON()
	msgCtx = append(msgCtx, []interface{}{
		"seconds_left", delta,
		"payload", string(payJSON),
		"expiration", notification.Expiration,
	}...)

	// TODO: refactor this mess
	if *notSend {
		pushRes.State = "NULL"
		conn.L.Info("message sent to /dev/null OK", msgCtx...)
		m.Finish()
		return pushRes, nil
	}

	var res *apns2.Response
	res, err = conn.Send(notification)
	msgCtx = append(msgCtx, "result", res)

	if err != nil {
		if err, ok := err.(net.Error); ok && err.Timeout() {
			pushRes.State = "timeout"
		} else {
			m.Finish()
			conn.L.Error("message send failed APNS Error: "+err.Error(), msgCtx...)
			pushRes.State = "failed"
			return pushRes, nil
		}
	} else if res.StatusCode >= 500 {
		err = errors.New(fmt.Sprintf("5xx status code (%v)", res.StatusCode))
		pushRes.State = "code_" + fmt.Sprintf("%v", res.StatusCode)
	}

	// resent on timeouts and 5xx errors
	// - https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/APNsProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH101-SW18
	if err != nil {
		if m.Attempts > 2 {
			m.Finish()
			conn.L.Error(
				fmt.Sprintf("message send failed APNS Error: %s on attempt %v", err.Error(), m.Attempts),
				msgCtx...)
			pushRes.State = "failed_" + pushRes.State
			return pushRes, nil
		}

		conn.L.Warn(
			fmt.Sprintf("Requeue attempt %v - APNS Error: %v", m.Attempts, err.Error()),
			msgCtx...)
		pushRes.State = "requeue_" + pushRes.State
		m.RequeueWithoutBackoff(time.Second)
		return pushRes, nil
	}

	if res.StatusCode != 200 {
		// err = errors.New(fmt.Sprintf("status code %v != 200", res.StatusCode))
		errLogMethod := h.L.Error
		errText := "message send result with issues"
		if res.StatusCode == 410 {
			errLogMethod = h.L.Warn
			errText = "The device token is no longer active for the topic"
			// FIXME: add helper with JSON encoding & error handling
			go h.SendFeedback(j.AppInfo.Token, msgCtx)
		} else if res.StatusCode == 400 && res.Reason == "BadDeviceToken" {
			errText = "Bad device token"
			// FIXME: add helper with JSON encoding & error handling
			go h.SendFeedback(j.AppInfo.Token, msgCtx)
		}
		pushRes.State = "failed_" + fmt.Sprintf("%v", res.StatusCode)
		//fmt.Sprintf("status code %v != 200", res.StatusCode)

		errLogMethod(errText, msgCtx...)
		m.Finish()
		return pushRes, nil
	}

	// if err != nil {
	// 	m.Finish()

	// 	errLogMethod(errText, msgCtx...)
	// 	return pushRes, nil
	// }

	pushRes.State = "sent"
	if m.Attempts > 1 {
		pushRes.State = "requeue_sent"
	}
	conn.L.Info("message sent OK", msgCtx...)
	m.Finish()
	return pushRes, nil
}
