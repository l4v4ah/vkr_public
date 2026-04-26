// Package nats wraps NATS JetStream for publishing and consuming telemetry messages.
package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	StreamName = "TELEMETRY"

	SubjectMetrics = "telemetry.metrics"
	SubjectLogs    = "telemetry.logs"
	SubjectSpans   = "telemetry.spans"
)

// Client wraps a NATS connection and a JetStream context.
type Client struct {
	conn *nats.Conn
	js   jetstream.JetStream
}

// Connect dials NATS and ensures the TELEMETRY stream exists.
func Connect(url string) (*Client, error) {
	conn, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats connect: %w", err)
	}

	js, err := jetstream.New(conn)
	if err != nil {
		return nil, fmt.Errorf("jetstream: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     StreamName,
		Subjects: []string{"telemetry.*"},
		MaxAge:   24 * time.Hour,
		Storage:  jetstream.FileStorage,
	})
	if err != nil {
		return nil, fmt.Errorf("create stream: %w", err)
	}

	return &Client{conn: conn, js: js}, nil
}

// Publish serialises v as JSON and publishes it to the given subject.
func (c *Client) Publish(ctx context.Context, subject string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = c.js.Publish(ctx, subject, data)
	return err
}

// Subscribe creates a durable push consumer and calls handler for each message.
// It blocks until ctx is cancelled.
func (c *Client) Subscribe(ctx context.Context, consumer, subject string, handler func([]byte) error) error {
	cons, err := c.js.CreateOrUpdateConsumer(ctx, StreamName, jetstream.ConsumerConfig{
		Durable:       consumer,
		FilterSubject: subject,
		AckPolicy:     jetstream.AckExplicitPolicy,
		DeliverPolicy: jetstream.DeliverAllPolicy,
		MaxAckPending: 512,
	})
	if err != nil {
		return fmt.Errorf("create consumer: %w", err)
	}

	cc, err := cons.Consume(func(msg jetstream.Msg) {
		if err := handler(msg.Data()); err != nil {
			_ = msg.Nak()
			return
		}
		_ = msg.Ack()
	})
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer cc.Stop()

	<-ctx.Done()
	return nil
}

// Close drains and closes the underlying NATS connection.
func (c *Client) Close() { _ = c.conn.Drain() }
