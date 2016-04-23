package config

import (
	"errors"

	nsq "github.com/nsqio/go-nsq"
)

// TomlConfig config from toml file
type TomlConfig struct {
	APNS    APNSConf               `toml:"APNS"`
	APNSapp map[string]APNSappConf `toml:"APNS-app"`
	NSQ     NsqConf                `toml:"nsq"`
}

// NsqConf NSQ configuration section
type NsqConf struct {
	MaxInFlight   int `toml:"max_in_flight"`
	Channel       string
	Topic         string
	FeedbackTopic string `toml:"feedback_topic"`
	Concurrency   int
	NsqdAddrs     []string `toml:"nsqd_addr"`
	LookupAddrs   []string `toml:"lookup_addr"`
	TTL           string
	LogLevel      string `toml:"log_level"`
}

// APNSConf main APNS vars
type APNSConf struct {
	TTL string
}

// APNSappConf config for one iOS app
type APNSappConf struct {
	Name       string
	KeyOpen    string `toml:"key_open"`
	KeyPrivate string `toml:"key_private"`
	// Sandbox    bool
}

// GetNSQLogLevel converts log level string to appropriate nsq.LogLevel
// https://godoc.org/github.com/nsqio/go-nsq#LogLevel.String
// https://godoc.org/github.com/inconshreveable/log15#LvlFromString
func GetNSQLogLevel(level string) (l nsq.LogLevel, e error) {
	switch level {
	case "debug", "dbug":
		l = nsq.LogLevelDebug
	case "info":
		l = nsq.LogLevelInfo
	case "warn":
		fallthrough
	case "warning":
		l = nsq.LogLevelWarning
	case "error", "eror":
		l = nsq.LogLevelError
	case "crit":
		l = nsq.LogLevelError
	default:
		e = errors.New("Unknown log level: " + level)
	}
	return
}
