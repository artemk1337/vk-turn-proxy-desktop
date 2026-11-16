// SPDX-License-Identifier: MIT

package main

import (
	"strconv"
	"strings"
	"time"
)

const trafficPrefix = "desktop-traffic "

type trafficSample struct {
	txBytes, rxBytes int64
	at               time.Time
	bytesPerSecond   float64
	txBytesPerSecond float64
	rxBytesPerSecond float64
}

func parseTraffic(line string) (int64, int64, bool) {
	data, ok := strings.CutPrefix(line, trafficPrefix)
	if !ok {
		return 0, 0, false
	}
	fields := strings.Fields(data)
	if len(fields) != 2 {
		return 0, 0, false
	}
	tx, txErr := strconv.ParseInt(fields[0], 10, 64)
	rx, rxErr := strconv.ParseInt(fields[1], 10, 64)
	return tx, rx, txErr == nil && rxErr == nil && tx >= 0 && rx >= 0
}

func (s *trafficSample) update(tx, rx int64, now time.Time) {
	txRate, rxRate := 0.0, 0.0
	if !s.at.IsZero() && now.After(s.at) && tx >= s.txBytes && rx >= s.rxBytes {
		seconds := now.Sub(s.at).Seconds()
		txRate = float64(tx-s.txBytes) / seconds
		rxRate = float64(rx-s.rxBytes) / seconds
	}
	*s = trafficSample{txBytes: tx, rxBytes: rx, at: now, bytesPerSecond: txRate + rxRate,
		txBytesPerSecond: txRate, rxBytesPerSecond: rxRate}
}

func (s trafficSample) rate(now time.Time) float64 {
	if s.at.IsZero() || now.Before(s.at) || now.Sub(s.at) > 4*time.Second {
		return 0
	}
	return s.bytesPerSecond
}
