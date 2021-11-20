package exporter

import (
	"strings"

	"github.com/gomodule/redigo/redis"
	log "github.com/sirupsen/logrus"
)

func (e *Exporter) configureOptions(uri string) ([]redis.DialOption, error) {
	tlsConfig, err := e.CreateClientTLSConfig()
	if err != nil {
		return nil, err
	}

	options := []redis.DialOption{
		redis.DialConnectTimeout(e.options.ConnectionTimeouts),
		redis.DialReadTimeout(e.options.ConnectionTimeouts),
		redis.DialWriteTimeout(e.options.ConnectionTimeouts),
		redis.DialTLSConfig(tlsConfig),
	}

	if e.options.Password != "" {
		options = append(options, redis.DialPassword(e.options.Password))
	}

	if e.options.PasswordMap[uri] != "" {
		options = append(options, redis.DialPassword(e.options.PasswordMap[uri]))
	}

	return options, nil
}

func (e *Exporter) connectToKvrocks() (redis.Conn, error) {
	uri := e.kvrocksAddr
	if !strings.Contains(uri, "://") {
		uri = "redis://" + uri
	}

	options, err := e.configureOptions(uri)
	if err != nil {
		return nil, err
	}

	log.Debugf("Trying DialURL(): %s", uri)
	c, err := redis.DialURL(uri, options...)
	if err != nil {
		log.Debugf("DialURL() failed, err: %s", err)
		if frags := strings.Split(e.kvrocksAddr, "://"); len(frags) == 2 {
			log.Debugf("Trying: Dial(): %s %s", frags[0], frags[1])
			c, err = redis.Dial(frags[0], frags[1], options...)
		} else {
			log.Debugf("Trying: Dial(): tcp %s", e.kvrocksAddr)
			c, err = redis.Dial("tcp", e.kvrocksAddr, options...)
		}
	}
	return c, err
}

func doRedisCmd(c redis.Conn, cmd string, args ...interface{}) (interface{}, error) {
	log.Debugf("c.Do() - running command: %s %s", cmd, args)
	res, err := c.Do(cmd, args...)
	if err != nil {
		log.Debugf("c.Do() - err: %s", err)
	}
	log.Debugf("c.Do() - done")
	return res, err
}
