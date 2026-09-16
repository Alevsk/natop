package report

import (
	"fmt"
	"io"

	"github.com/alevsk/natop/internal/monitor"
)

// Thresholds are health limits evaluated in --once mode. Zero disables the
// corresponding check.
type Thresholds struct {
	MaxPending     uint64
	MaxAckPending  uint64
	MaxRedelivered uint64
}

// Breach is one connection or consumer metric that failed a health check.
type Breach struct {
	Connection string
	Stream     string
	Consumer   string
	Metric     string
	Value      uint64
	Limit      uint64
	Detail     string // extra context, e.g. a non-online connection's status/error
}

// Check evaluates every connection's status and every consumer's counters
// against t, returning every breach found. A non-online connection is always
// a breach regardless of thresholds: a fleet health check that ignores dead
// connections is useless.
func Check(snapshots []monitor.Snapshot, t Thresholds) []Breach {
	var breaches []Breach
	for _, s := range snapshots {
		if s.Status != "online" {
			detail := s.Status
			if s.Error != "" {
				detail = s.Status + ": " + s.Error
			}
			breaches = append(breaches, Breach{Connection: s.Name, Metric: "connection_status", Value: 1, Detail: detail})
		}
		for _, stream := range s.Streams {
			if stream.Info == nil {
				continue
			}
			name := stream.Info.Config.Name
			for _, c := range stream.Consumers {
				if t.MaxPending > 0 && c.NumPending > t.MaxPending {
					breaches = append(breaches, Breach{Connection: s.Name, Stream: name, Consumer: c.Name, Metric: "pending", Value: c.NumPending, Limit: t.MaxPending})
				}
				if ap := uint64(max(0, c.NumAckPending)); t.MaxAckPending > 0 && ap > t.MaxAckPending {
					breaches = append(breaches, Breach{Connection: s.Name, Stream: name, Consumer: c.Name, Metric: "ack_pending", Value: ap, Limit: t.MaxAckPending})
				}
				if rd := uint64(max(0, c.NumRedelivered)); t.MaxRedelivered > 0 && rd > t.MaxRedelivered {
					breaches = append(breaches, Breach{Connection: s.Name, Stream: name, Consumer: c.Name, Metric: "redelivered", Value: rd, Limit: t.MaxRedelivered})
				}
			}
		}
	}
	return breaches
}

// WriteBreaches renders one greppable "breach key=value ..." line per
// finding, meant for stderr so a failing --once run says why.
func WriteBreaches(w io.Writer, breaches []Breach) error {
	for _, b := range breaches {
		if _, err := fmt.Fprintf(w, "breach connection=%s stream=%s consumer=%s metric=%s value=%d limit=%d detail=%q\n",
			b.Connection, b.Stream, b.Consumer, b.Metric, b.Value, b.Limit, b.Detail); err != nil {
			return err
		}
	}
	return nil
}
