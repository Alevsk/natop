package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alevsk/natop/internal/config"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// maxConcurrentConsumerLookups bounds in-flight consumer lookups per Fetch pass
// so a deployment with hundreds of streams doesn't exhaust the outer 15s budget
// waiting on them sequentially, without opening unbounded connections at once.
const maxConcurrentConsumerLookups = 16

// Client is owned by one polling worker. Only the NATS error callback runs
// concurrently; its diagnostic is protected separately.
type Client struct {
	config     config.Connection
	timeout    time.Duration
	nc         *nats.Conn
	js         jetstream.JetStream
	mu         sync.Mutex
	asyncError string
}

func NewClient(c config.Connection, timeout time.Duration) *Client {
	return &Client{config: c, timeout: timeout}
}

func (c *Client) Close() {
	if c.nc != nil {
		c.nc.Close()
	}
}

func (c *Client) connect() error {
	if err := c.config.Validate(); err != nil {
		return err
	}
	opts := []nats.Option{
		nats.Name("natop"), nats.Timeout(c.timeout),
		nats.MaxReconnects(-1), nats.ReconnectWait(time.Second),
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.asyncError = c.config.Redact(err.Error())
		}),
	}
	if c.config.Credentials != "" {
		opts = append(opts, nats.UserCredentials(c.config.Credentials))
	}
	if c.config.Token != "" {
		opts = append(opts, nats.Token(c.config.Token))
	}
	if c.config.User != "" {
		opts = append(opts, nats.UserInfo(c.config.User, c.config.Password))
	}
	if c.config.TLSCA != "" {
		opts = append(opts, nats.RootCAs(c.config.TLSCA))
	}
	if c.config.TLSCert != "" {
		opts = append(opts, nats.ClientCert(c.config.TLSCert, c.config.TLSKey))
	}
	nc, err := nats.Connect(c.config.URL, opts...)
	if err != nil {
		return err
	}
	var js jetstream.JetStream
	if c.config.Domain != "" {
		js, err = jetstream.NewWithDomain(nc, c.config.Domain)
	} else {
		js, err = jetstream.New(nc)
	}
	if err != nil {
		nc.Close()
		return err
	}
	c.nc, c.js = nc, js
	return nil
}

func (c *Client) diagnostic(err error) string {
	message := err.Error()
	if errors.Is(err, nats.ErrNoResponders) || errors.Is(err, jetstream.ErrJetStreamNotEnabled) {
		message = "JetStream unavailable: check that it is enabled for this account and domain"
	}
	c.mu.Lock()
	extra := c.asyncError
	c.mu.Unlock()
	if extra != "" && !strings.Contains(message, extra) {
		message += "; " + extra
	}
	return c.config.Redact(message)
}

// Fetch lists metadata only. It never creates or binds a consuming subscription.
// Failed lists retain the previous data; successful empty lists replace it.
func (c *Client) Fetch(ctx context.Context, previous Snapshot, needsConsumers bool) Snapshot {
	result := previous
	result.Name, result.URL = c.config.Name, c.config.SafeURL()
	result.Status, result.Error = "offline", ""
	c.mu.Lock()
	c.asyncError = ""
	c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		return result
	}
	if c.nc == nil || c.nc.IsClosed() {
		if err := c.connect(); err != nil {
			result.Error = c.diagnostic(err)
			return result
		}
	}
	if !c.nc.IsConnected() {
		result.Error = "Disconnected; reconnecting automatically"
		return result
	}
	// Bound a complete pass as well as each individual list request.
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	listCtx, listCancel := context.WithTimeout(ctx, c.timeout)
	lister := c.js.ListStreams(listCtx)
	var infos []*jetstream.StreamInfo
	for info := range lister.Info() {
		infos = append(infos, info)
	}
	err := lister.Err()
	listCancel()
	if err != nil {
		result.Status = "error"
		result.Error = c.diagnostic(err)
		return result
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Config.Name < infos[j].Config.Name })
	old := make(map[string]Stream, len(previous.Streams))
	for _, stream := range previous.Streams {
		old[stream.Info.Config.Name] = stream
	}
	result.Streams = make([]Stream, len(infos))
	result.Status = "online"
	var failed atomic.Int32
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxConcurrentConsumerLookups)
	for i, info := range infos {
		stream := old[info.Config.Name]
		// A recreated stream must not inherit consumers from its predecessor.
		if stream.Info != nil && !stream.Info.Created.Equal(info.Created) {
			stream = Stream{}
		}
		stream.Info, stream.Updated, stream.Error = info, time.Now(), ""
		result.Streams[i] = stream

		if !needsConsumers || info.State.Consumers == 0 {
			result.Streams[i].Consumers = nil
			result.Streams[i].ConsumersUpdated = time.Now()
			continue
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(i int, name string) {
			defer wg.Done()
			defer func() { <-sem }()
			consumers, err := c.consumers(ctx, name)
			if err != nil {
				result.Streams[i].Error = c.diagnostic(err)
				failed.Add(1)
			} else {
				result.Streams[i].Consumers, result.Streams[i].ConsumersUpdated = consumers, time.Now()
			}
		}(i, info.Config.Name)
	}
	wg.Wait()
	if n := failed.Load(); n > 0 {
		result.Status = "partial"
		result.Error = fmt.Sprintf("Consumer metadata unavailable for %d stream(s); select the stream for details", n)
	} else {
		result.Updated = time.Now()
	}
	return result
}

func (c *Client) consumers(ctx context.Context, name string) ([]*jetstream.ConsumerInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	stream, err := c.js.Stream(ctx, name)
	if err != nil {
		return nil, err
	}
	lister := stream.ListConsumers(ctx)
	var consumers []*jetstream.ConsumerInfo
	for info := range lister.Info() {
		consumers = append(consumers, info)
	}
	if err := lister.Err(); err != nil {
		return nil, err
	}
	sort.Slice(consumers, func(i, j int) bool { return consumers[i].Name < consumers[j].Name })
	return consumers, nil
}
