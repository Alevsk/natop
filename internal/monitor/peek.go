package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alevsk/natop/internal/config"
	"github.com/nats-io/nats.go/jetstream"
)

func (m *Manager) PeekMessages(ctx context.Context, connectionName, streamName string, startSeq uint64, count int) ([]*jetstream.RawStreamMsg, error) {
	if m.demo {
		msgs := make([]*jetstream.RawStreamMsg, 0, count)
		for i := 0; i < count; i++ {
			msgs = append(msgs, &jetstream.RawStreamMsg{
				Sequence: startSeq + uint64(i),
				Subject:  fmt.Sprintf("%s.demo.subject", streamName),
				Time:     time.Now().Add(-time.Duration(count-i) * time.Second),
				Data:     []byte(fmt.Sprintf(`{"demo_message": %d, "hello": "world"}`, startSeq+uint64(i))),
			})
		}
		return msgs, nil
	}

	var connConfig *config.Connection
	for _, c := range m.config.Connections {
		if c.Name == connectionName {
			connConfig = &c
			break
		}
	}
	if connConfig == nil {
		return nil, errors.New("connection not found")
	}

	client := NewClient(*connConfig, 2*time.Second)
	if err := client.connect(); err != nil {
		return nil, err
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stream, err := client.js.Stream(ctx, streamName)
	if err != nil {
		return nil, err
	}

	var msgs []*jetstream.RawStreamMsg
	seq := startSeq
	for i := 0; i < count; {
		msg, err := stream.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				seq++
				// if we have lots of gaps, we might loop a lot. Cap the lookahead.
				if seq > startSeq+uint64(count*10) {
					break
				}
				continue
			}
			// some other error, just break and return what we have, or error if empty
			if len(msgs) == 0 {
				return nil, err
			}
			break
		}
		msgs = append(msgs, msg)
		seq++
		i++
	}
	return msgs, nil
}

func (m *Manager) PeekMessagesBackward(ctx context.Context, connectionName, streamName string, endSeq uint64, count int) ([]*jetstream.RawStreamMsg, error) {
	if m.demo {
		msgs := make([]*jetstream.RawStreamMsg, 0, count)
		if endSeq <= 1 {
			return msgs, nil
		}
		start := endSeq - 1
		if start < uint64(count) {
			start = uint64(count) // clamp so we don't underflow if we just want up to count
		}
		for i := count - 1; i >= 0; i-- {
			seq := start - uint64(i)
			if seq < 1 {
				continue
			}
			msgs = append(msgs, &jetstream.RawStreamMsg{
				Sequence: seq,
				Subject:  fmt.Sprintf("%s.demo.subject", streamName),
				Time:     time.Now().Add(-time.Duration(i) * time.Second),
				Data:     []byte(fmt.Sprintf(`{"demo_message": %d, "hello": "world"}`, seq)),
			})
		}
		return msgs, nil
	}

	var connConfig *config.Connection
	for _, c := range m.config.Connections {
		if c.Name == connectionName {
			connConfig = &c
			break
		}
	}
	if connConfig == nil {
		return nil, errors.New("connection not found")
	}

	client := NewClient(*connConfig, 2*time.Second)
	if err := client.connect(); err != nil {
		return nil, err
	}
	defer client.Close()

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	stream, err := client.js.Stream(ctx, streamName)
	if err != nil {
		return nil, err
	}

	var msgs []*jetstream.RawStreamMsg
	if endSeq <= 1 {
		return msgs, nil
	}
	seq := endSeq - 1
	for i := 0; i < count; {
		msg, err := stream.GetMsg(ctx, seq)
		if err != nil {
			if errors.Is(err, jetstream.ErrMsgNotFound) {
				if seq <= 1 || endSeq-seq > uint64(count*10) {
					break // reached start of stream or lookbehind limit
				}
				seq--
				continue
			}
			if len(msgs) == 0 {
				return nil, err
			}
			break
		}
		// Prepend msg to maintain ascending order
		msgs = append([]*jetstream.RawStreamMsg{msg}, msgs...)
		if seq <= 1 {
			break
		}
		seq--
		i++
	}
	return msgs, nil
}
