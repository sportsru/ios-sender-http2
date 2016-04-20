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

// HandleMessage process NSQ messages
// more info: https://godoc.org/github.com/nsqio/go-nsq#HandlerFunc.HandleMessage
func (h *Hub) HandleMessage(m *nsq.Message) error {
	var err error

	stat := h.MessagesStat
	counter := atomic.AddInt64(&stat.total, 1)

	// TODO: if *debug {
	h.L.Debug("handle NSQ message", "counter", counter)
	defer h.L.Debug("finished NSQ message", "counter", counter)
	// }
	m.DisableAutoResponse()

	// http://blog.golang.org/json-and-go
	j := &NSQpush{}
	err = json.Unmarshal(m.Body, &j)
	if err != nil {
		h.L.Error("failed to parse JSON: "+err.Error(), "body", string(m.Body))
		atomic.AddInt64(&stat.drop, 1)
		m.Finish()
		return nil
	}

	name := j.AppInfo.AppBundleName
	// TODO: add support of
	// if j.AppInfo.Sandbox {
	// 	name = name + "-dev"
	// }

	// Check Application name
	conn, ok := h.Consumers[name]
	if !ok {
		m.Finish()
		atomic.AddInt64(&stat.skip, 1)
		h.L.Debug("appname not found, skip message", "appname", name)
		// MAYBE: add stat for unknown apps?
		return nil
	}
	cStat := conn.Stat

	// // Prepare logging context
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

		atomic.AddInt64(&cStat.Drop, 1)
		atomic.AddInt64(&stat.drop, 1)

		msgTTLCtx := append(msgCtx, []interface{}{
			"TTL", h.ProducerTTL,
			"timestamp", jTs,
			"delta", delta,
			"body", string(m.Body),
		}...)
		h.L.Warn("message TTL is over", msgTTLCtx...)
		return nil
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

	var res *apns2.Response
	msg := "message sent OK"
	if !*notSend {
		res, err = conn.Send(notification)
		// _ = res
		msgCtx = append(msgCtx, "result", res)
		var errText string
		errLogMethod := h.L.Error
		if err != nil {
			errText = "message send failed APNS Error: " + err.Error()
		} else if res.StatusCode != 200 {
			err = errors.New(fmt.Sprintf("status code %v != 200", res.StatusCode))
			//errLogMethod = h.L.Warn
			errText = "message send result with issues"
			if res.StatusCode == 410 {
				errLogMethod = h.L.Warn
				errText = "The device token is no longer active for the topic"
				// FIXME: add helper with JSON encoding & error handling
				h.SendFeedback(j.AppInfo.Token, msgCtx)
			} else if res.StatusCode == 400 && res.Reason == "BadDeviceToken" {
				errText = "Bad device token"
				// FIXME: add helper with JSON encoding & error handling
				h.SendFeedback(j.AppInfo.Token, msgCtx)
			}
			// TODO: deliberate resent on 503
			// https://developer.apple.com/library/ios/documentation/NetworkingInternet/Conceptual/RemoteNotificationsPG/Chapters/APNsProviderAPI.html#//apple_ref/doc/uid/TP40008194-CH101-SW18
		}

		if err != nil {
			m.Finish()

			atomic.AddInt64(&cStat.Drop, 1)
			atomic.AddInt64(&stat.drop, 1)
			errLogMethod(errText, msgCtx...)
			return nil
		}
		atomic.AddInt64(&cStat.Send, 1)
		atomic.AddInt64(&stat.send, 1)
	} else {
		atomic.AddInt64(&cStat.Skip, 1)
		atomic.AddInt64(&stat.skip, 1)
		msg = "message sent to /dev/null OK"
	}
	h.L.Info(msg, msgCtx...)

	m.Finish()
	return nil
}
