package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds runtime configuration for the PSU monitor service.
type Config struct {
	NodeID   string
	ServerID string

	// iDRAC connection
	IDRACHost     string
	IDRACPort     int
	IDRACUsername string
	IDRACPassword string

	// MQTT broker (AmazonMQ)
	MQTTBroker   string
	MQTTPort     int
	MQTTUsername string
	MQTTPassword string
	MQTTClientID string

	// Collection interval (default 1s for demo responsiveness)
	PollInterval time.Duration

	LogLevel string
}

// Load reads configuration from environment variables.
func Load() (*Config, error) {
	hostname, _ := os.Hostname()
	nodeID := envOr("NODE_ID", hostname)

	mqttPort, _ := strconv.Atoi(envOr("MQTT_PORT", "8883"))
	idracPort, _ := strconv.Atoi(envOr("IDRAC_PORT", "443"))
	pollSec, _ := strconv.Atoi(envOr("POLL_INTERVAL", "1"))

	idracHost := envOr("IDRAC_HOST", "")
	if idracHost == "" {
		return nil, fmt.Errorf("IDRAC_HOST environment variable is required")
	}

	return &Config{
		NodeID:        nodeID,
		ServerID:      envOr("SERVER_ID", fmt.Sprintf("SRV-%s", nodeID)),
		IDRACHost:     idracHost,
		IDRACPort:     idracPort,
		IDRACUsername: envOr("IDRAC_USERNAME", "root"),
		IDRACPassword: envOr("IDRAC_PASSWORD", "@irswift11!"),
		MQTTBroker:    envOr("MQTT_BROKER", "b-39377c99-a393-42a9-bf96-49810e3f9bbb-1.mq.us-east-1.amazonaws.com"),
		MQTTPort:      mqttPort,
		MQTTUsername:  envOr("MQTT_USERNAME", "carbonite"),
		MQTTPassword:  envOr("MQTT_PASSWORD", "AirswiftAirswift11!"),
		MQTTClientID:  fmt.Sprintf("psu-monitor-%s", nodeID),
		PollInterval:  time.Duration(pollSec) * time.Second,
		LogLevel:      envOr("LOG_LEVEL", "info"),
	}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
