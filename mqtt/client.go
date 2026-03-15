package mqtt

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"

	"github.com/r6dev-cadams/greengrass-services/health-check/config"
)

// Client wraps the Paho MQTT client for the health-check service.
type Client struct {
	client pahomqtt.Client
	cfg    *config.Config
}

// NewClient creates and connects an MQTT client to the AmazonMQ broker.
func NewClient(cfg *config.Config) (*Client, error) {
	opts := pahomqtt.NewClientOptions()

	broker := fmt.Sprintf("ssl://%s:%d", cfg.MQTTBroker, cfg.MQTTPort)
	opts.AddBroker(broker)
	opts.SetClientID(cfg.MQTTClientID)
	opts.SetUsername(cfg.MQTTUsername)
	opts.SetPassword(cfg.MQTTPassword)

	opts.SetTLSConfig(&tls.Config{
		MinVersion: tls.VersionTLS12,
	})

	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetConnectRetryInterval(5 * time.Second)
	opts.SetMaxReconnectInterval(30 * time.Second)
	opts.SetKeepAlive(60 * time.Second)
	opts.SetCleanSession(false)

	opts.SetOnConnectHandler(func(_ pahomqtt.Client) {
		slog.Info("mqtt connected", "broker", broker, "clientId", cfg.MQTTClientID)
	})
	opts.SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
		slog.Warn("mqtt connection lost, will auto-reconnect", "error", err)
	})
	opts.SetReconnectingHandler(func(_ pahomqtt.Client, _ *pahomqtt.ClientOptions) {
		slog.Info("mqtt reconnecting...", "broker", broker)
	})

	pahoClient := pahomqtt.NewClient(opts)

	token := pahoClient.Connect()
	if !token.WaitTimeout(30 * time.Second) {
		return nil, fmt.Errorf("mqtt connect timeout after 30s to %s", broker)
	}
	if token.Error() != nil {
		return nil, fmt.Errorf("mqtt connect to %s: %w", broker, token.Error())
	}

	return &Client{client: pahoClient, cfg: cfg}, nil
}

// PublishJSON marshals v to JSON and publishes to the given topic with QoS 0.
func (c *Client) PublishJSON(topic string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal payload for %s: %w", topic, err)
	}

	token := c.client.Publish(topic, 0, false, payload)
	token.Wait()
	if token.Error() != nil {
		return fmt.Errorf("publish to %s: %w", topic, token.Error())
	}
	return nil
}

// Disconnect gracefully disconnects from the MQTT broker.
func (c *Client) Disconnect() {
	slog.Info("disconnecting mqtt client")
	c.client.Disconnect(5000)
}
