// Package redisconfig shares validated Redis connections across runtime and operator commands.
package redisconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/redis/go-redis/v9"
)

func Options(getenv func(string) string, prefix, defaultAddr string) (*redis.Options, error) {
	o := &redis.Options{Addr: getenv(prefix + "ADDR"), Username: getenv(prefix + "USERNAME"), Protocol: 2, MaxRetries: 2}
	if o.Addr == "" {
		o.Addr = defaultAddr
	}
	password, err := Secret(getenv, prefix+"PASSWORD")
	if err != nil {
		return nil, err
	}
	o.Password = password
	mode := getenv(prefix + "TLS")
	if mode != "" && mode != "true" && mode != "false" {
		return nil, fmt.Errorf("%sTLS must be true or false", prefix)
	}
	ca, cert, key := getenv(prefix+"TLS_CA"), getenv(prefix+"TLS_CERT"), getenv(prefix+"TLS_KEY")
	if mode != "true" {
		if ca != "" || cert != "" || key != "" {
			return nil, fmt.Errorf("%sTLS is required for certificates", prefix)
		}
		return o, nil
	}
	o.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, ServerName: getenv(prefix + "TLS_SERVER_NAME")}
	if ca != "" {
		pem, err := os.ReadFile(ca)
		if err != nil {
			return nil, fmt.Errorf("read Redis CA: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("invalid Redis CA")
		}
		o.TLSConfig.RootCAs = pool
	}
	if (cert == "") != (key == "") {
		return nil, fmt.Errorf("Redis client certificate and key must both be set")
	}
	if cert != "" {
		pair, err := tls.LoadX509KeyPair(cert, key)
		if err != nil {
			return nil, fmt.Errorf("load Redis client certificate: %w", err)
		}
		o.TLSConfig.Certificates = []tls.Certificate{pair}
	}
	return o, nil
}

func Secret(getenv func(string) string, name string) (string, error) {
	value, file := getenv(name), getenv(name+"_FILE")
	if value != "" && file != "" {
		return "", fmt.Errorf("set only %s or %s_FILE", name, name)
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("read %s_FILE: %w", name, err)
		}
		value = strings.TrimSpace(string(data))
		if value == "" {
			return "", fmt.Errorf("%s_FILE is empty", name)
		}
	}
	return value, nil
}
