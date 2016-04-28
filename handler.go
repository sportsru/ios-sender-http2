package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

// HandleMessage process NSQ messages
// more info: https://godoc.org/github.com/nsqio/go-nsq#HandlerFunc.HandleMessage
func (h *Hub) HandleMessageOld(m *nsq.Message) error {
	var err error
	res, err := h.sendMessage(m)
	if err != nil {
		if ctxErr, ok := err.(ErrorWithContext); ok {
			h.L.Error(ctxErr.Msg, ctxErr.Ctx...)
		} else {
			h.L.Error(err.Error())
		}
	}
	if res != nil {
		h.L.Info("res != nil")
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
	// if j.AppInfo.Sandbox {
	// 	name = name + "-dev"
	// }

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
		"id", counter,
		"app", name,
		"token", j.AppInfo.Token,
	}
	h.L.Debug("start process message", msgCtx...)

	// Check TTL
	nowUnix := time.Now().UTC().Unix()
	jTs := j.DbRes.Timestamp
	delta := nowUnix - (jTs + h.ProducerTTL)
	if delta > 0 {
		m.Finish()

		pushRes.State = "ttl_is_over"
		msgTTLCtx := append(msgCtx, []interface{}{
			"TTL", h.ProducerTTL,
			"timestamp", jTs,
			"delta", delta,
			"body", string(m.Body),
		}...)
		h.L.Warn("message TTL is over", msgTTLCtx...)
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
	}...)

	// TODO: refactor this mess
	var res *apns2.Response
	msg := "message sent OK"
	if *notSend {
		msg = "message sent to /dev/null OK"
		pushRes.State = "NULL"
	} else {
		res, err = conn.Send(notification)
		msgCtx = append(msgCtx, "result", res)
		var errText string
		errLogMethod := h.L.Error
		if err != nil {
			pushRes.State = "failed"
			errText = "message send failed APNS Error: " + err.Error()
		} else if res.StatusCode != 200 {
			err = errors.New(fmt.Sprintf("status code %v != 200", res.StatusCode))
			//errLogMethod = h.L.Warn
			errText = "message send result with issues"
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
			// TODO: deliberate resent on 503
			// https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/APNsProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH101-SW18
		}

		if err != nil {
			m.Finish()

			errLogMethod(errText, msgCtx...)
			return pushRes, nil
		}
	}
	pushRes.State = "sent"
	h.L.Info(msg, msgCtx...)

	m.Finish()
	return pushRes, nil
}
