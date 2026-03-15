package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds all runtime configuration for the health-check service.
type Config struct {
	// Node identity
	NodeID string

	// MQTT broker settings (AmazonMQ)
	MQTTBroker   string
	MQTTPort     int
	MQTTUsername string
	MQTTPassword string
	MQTTClientID string

	// Collection settings
	PublishInterval time.Duration

	// Logging
	LogLevel string
}

// Load reads configuration from environment variables with sensible defaults.
func Load() (*Config, error) {
	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}

	nodeID := envOr("NODE_ID", hostname)

	port, _ := strconv.Atoi(envOr("MQTT_PORT", "8883"))
	interval, _ := strconv.Atoi(envOr("PUBLISH_INTERVAL", "5"))

	cfg := &Config{
		NodeID:          nodeID,
		MQTTBroker:      envOr("MQTT_BROKER", "b-39377c99-a393-42a9-bf96-49810e3f9bbb-1.mq.us-east-1.amazonaws.com"),
		MQTTPort:        port,
		MQTTUsername:     envOr("MQTT_USERNAME", "carbonite"),
		MQTTPassword:     envOr("MQTT_PASSWORD", "AirswiftAirswift11!"),
		MQTTClientID:    fmt.Sprintf("health-check-%s", nodeID),
		PublishInterval:  time.Duration(interval) * time.Second,
		LogLevel:        envOr("LOG_LEVEL", "info"),
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
