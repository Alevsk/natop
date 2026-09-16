package monitor

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/alevsk/natop/internal/config"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func testServer(t *testing.T, opts *server.Options) *server.Server {
	t.Helper()
	if opts == nil {
		opts = &server.Options{}
	}
	opts.Host = "127.0.0.1"
	if opts.Port == 0 {
		opts.Port = -1
	}
	opts.JetStream, opts.NoLog, opts.NoSigs = true, true, true
	if opts.StoreDir == "" {
		opts.StoreDir = t.TempDir()
	}
	s, err := server.NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	go s.Start()
	if !s.ReadyForConnections(5 * time.Second) {
		t.Fatal("server did not start")
	}
	t.Cleanup(func() { s.Shutdown(); s.WaitForShutdown() })
	return s
}

func seed(t *testing.T, url string) (jetstream.JetStream, jetstream.Consumer) {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_, err = js.CreateStream(ctx, jetstream.StreamConfig{Name: "WORK", Subjects: []string{"work.>"}, Storage: jetstream.MemoryStorage})
	if err != nil {
		t.Fatal(err)
	}
	c, err := js.CreateConsumer(ctx, "WORK", jetstream.ConsumerConfig{Durable: "worker", AckPolicy: jetstream.AckExplicitPolicy, AckWait: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err = js.Publish(ctx, "work.new", []byte("hello")); err != nil {
			t.Fatal(err)
		}
	}
	batch, err := c.Fetch(1, jetstream.FetchMaxWait(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for range batch.Messages() {
	} // Deliberately leave one message awaiting acknowledgment.
	if batch.Error() != nil {
		t.Fatal(batch.Error())
	}
	return js, c
}

func TestFetchReportsMetadataWithoutChangingConsumer(t *testing.T) {
	s := testServer(t, nil)
	js, consumer := seed(t, s.ClientURL())
	ctx := context.Background()
	before, _ := consumer.Info(ctx)
	c := NewClient(config.Connection{Name: "local", URL: s.ClientURL()}, time.Second)
	defer c.Close()
	snapshot := c.Fetch(ctx, Snapshot{})
	if snapshot.Error != "" || snapshot.Status != "online" {
		t.Fatalf("fetch: %+v", snapshot)
	}
	if len(snapshot.Streams) != 1 {
		t.Fatalf("streams: %+v", snapshot.Streams)
	}
	stream := snapshot.Streams[0]
	if stream.Info.State.Msgs != 3 || len(stream.Consumers) != 1 {
		t.Fatalf("stream: %+v", stream)
	}
	if got := stream.Consumers[0]; got.NumPending != 2 || got.NumAckPending != 1 {
		t.Fatalf("consumer: %+v", got)
	}
	after, _ := consumer.Info(ctx)
	if !reflect.DeepEqual(before.Delivered, after.Delivered) || !reflect.DeepEqual(before.AckFloor, after.AckFloor) || before.NumPending != after.NumPending || before.NumAckPending != after.NumAckPending {
		t.Fatal("monitor changed consumer state")
	}
	if err := js.DeleteStream(ctx, "WORK"); err != nil {
		t.Fatal(err)
	}
	empty := c.Fetch(ctx, snapshot)
	if len(empty.Streams) != 0 || empty.Error != "" {
		t.Fatalf("successful empty fetch retained old data: %+v", empty)
	}
}

func TestFailedRefreshRetainsStaleData(t *testing.T) {
	s := testServer(t, nil)
	seed(t, s.ClientURL())
	c := NewClient(config.Connection{Name: "local", URL: s.ClientURL()}, 100*time.Millisecond)
	defer c.Close()
	good := c.Fetch(context.Background(), Snapshot{})
	if good.Error != "" {
		t.Fatal(good.Error)
	}
	s.Shutdown()
	s.WaitForShutdown()
	bad := c.Fetch(context.Background(), good)
	if bad.Error == "" || bad.Status == "online" {
		t.Fatalf("missing failure: %+v", bad)
	}
	if len(bad.Streams) != 1 || !bad.Updated.Equal(good.Updated) {
		t.Fatal("failure erased old snapshot or changed its timestamp")
	}
}

// seedMany creates n streams named S00..Sn-1 so their sorted order is stable,
// giving a durable consumer to every third one.
func seedMany(t *testing.T, url string, n int) []string {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(nc.Close)
	js, err := jetstream.New(nc)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	names := make([]string, n)
	for i := range n {
		name := fmt.Sprintf("S%02d", i)
		names[i] = name
		if _, err := js.CreateStream(ctx, jetstream.StreamConfig{Name: name, Subjects: []string{name + ".>"}, Storage: jetstream.MemoryStorage}); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			if _, err := js.CreateConsumer(ctx, name, jetstream.ConsumerConfig{Durable: "worker", AckPolicy: jetstream.AckExplicitPolicy, AckWait: time.Hour}); err != nil {
				t.Fatal(err)
			}
		}
	}
	return names
}

func TestFetchHandlesManyStreamsConcurrently(t *testing.T) {
	s := testServer(t, nil)
	names := seedMany(t, s.ClientURL(), 24)
	sort.Strings(names)
	c := NewClient(config.Connection{Name: "local", URL: s.ClientURL()}, time.Second)
	defer c.Close()
	snapshot := c.Fetch(context.Background(), Snapshot{})
	if snapshot.Error != "" || snapshot.Status != "online" {
		t.Fatalf("fetch: %+v", snapshot)
	}
	if len(snapshot.Streams) != len(names) {
		t.Fatalf("streams: got %d want %d", len(snapshot.Streams), len(names))
	}
	for i, stream := range snapshot.Streams {
		if stream.Info.Config.Name != names[i] {
			t.Fatalf("stream %d out of order: got %s want %s", i, stream.Info.Config.Name, names[i])
		}
		if stream.Error != "" {
			t.Fatalf("stream %s: %s", names[i], stream.Error)
		}
		wantConsumers := i%3 == 0
		if hasConsumers := len(stream.Consumers) == 1; hasConsumers != wantConsumers {
			t.Fatalf("stream %s: got %d consumers, want consumer=%v", names[i], len(stream.Consumers), wantConsumers)
		}
	}
}

func TestManagerDoesNotWaitForFailedConnection(t *testing.T) {
	s := testServer(t, nil)
	seed(t, s.ClientURL())
	m := NewManager(config.Config{Refresh: 250 * time.Millisecond, Connections: []config.Connection{
		{Name: "down", URL: "nats://127.0.0.1:1"}, {Name: "good", URL: s.ClientURL()},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	updates := m.Start(ctx)
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	seen := map[string]bool{}
	for !seen["good"] || !seen["down"] {
		select {
		case snapshot := <-updates:
			if snapshot.Name == "good" && snapshot.Status == "online" && len(snapshot.Streams) == 1 {
				seen["good"] = true
			}
			if snapshot.Name == "down" && snapshot.Error != "" {
				seen["down"] = true
			}
		case <-timeout.C:
			t.Fatalf("connections not updated independently: %v", seen)
		}
	}
	cancel()
	select {
	case <-m.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("manager did not stop")
	}
}
