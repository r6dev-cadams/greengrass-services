package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/r6dev-cadams/greengrass-services/health-check/collector"
	"github.com/r6dev-cadams/greengrass-services/health-check/config"
	"github.com/r6dev-cadams/greengrass-services/health-check/mqtt"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	// ---- 1. Structured logger ----
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("health-check service starting",
		"version", Version,
		"pid", os.Getpid())

	// ---- 2. Load configuration ----
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration error", "error", err)
		os.Exit(1)
	}

	if cfg.LogLevel == "debug" {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: slog.LevelDebug,
		})))
	}

	slog.Info("configuration loaded",
		"nodeId", cfg.NodeID,
		"mqttBroker", cfg.MQTTBroker,
		"publishInterval", cfg.PublishInterval)

	// ---- 3. Context with cancellation ----
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ---- 4. Initialize MQTT client ----
	mqttClient, err := mqtt.NewClient(cfg)
	if err != nil {
		slog.Error("MQTT connection failed", "error", err)
		os.Exit(1)
	}
	defer mqttClient.Disconnect()

	// ---- 5. Initialize system collector ----
	coll := collector.New(cfg.NodeID)

	// ---- 6. Start publish loop ----
	go publishLoop(ctx, cfg, coll, mqttClient)

	// ---- 7. Wait for shutdown signal ----
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigChan
	slog.Info("shutdown signal received", "signal", sig)

	cancel()
	time.Sleep(1 * time.Second)
	slog.Info("health-check service stopped", "nodeId", cfg.NodeID)
}

// publishLoop runs the periodic health-check publish cycle.
func publishLoop(ctx context.Context, cfg *config.Config,
	coll *collector.Collector, mqttClient *mqtt.Client) {

	slog.Info("publish loop started",
		"interval", cfg.PublishInterval,
		"nodeId", cfg.NodeID)

	// Immediate first publish
	publishAll(coll, mqttClient)

	ticker := time.NewTicker(cfg.PublishInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("publish loop stopped")
			return
		case <-ticker.C:
			publishAll(coll, mqttClient)
		}
	}
}

// publishAll collects and publishes all four MQTT topics.
func publishAll(coll *collector.Collector, mqttClient *mqtt.Client) {
	// 1) Device status
	if err := mqttClient.PublishJSON("greengrass/device/status", coll.CollectStatus()); err != nil {
		slog.Error("publish status failed", "error", err)
	}

	// 2) Report center
	if err := mqttClient.PublishJSON("greengrass/device/reportcenter", coll.CollectReportCenter()); err != nil {
		slog.Error("publish reportcenter failed", "error", err)
	}

	// 3) WiFi address
	if err := mqttClient.PublishJSON("greengrass/device/wifiaddr", coll.CollectWifiAddr()); err != nil {
		slog.Error("publish wifiaddr failed", "error", err)
	}

	// 4) Full system info
	if err := mqttClient.PublishJSON("greengrass/device/sysinfo", coll.CollectSysinfo()); err != nil {
		slog.Error("publish sysinfo failed", "error", err)
	}

	// 5) Report data (warning file contents, if present)
	if warnData := coll.CollectReportDataWarn(); warnData != nil {
		if err := mqttClient.PublishJSON("greengrass/device/reportdatawarn", warnData); err != nil {
			slog.Error("publish reportdatawarn failed", "error", err)
		}
	}

	// 6) Report data (error file contents, if present)
	if errData := coll.CollectReportDataErr(); errData != nil {
		if err := mqttClient.PublishJSON("greengrass/device/reportdataerr", errData); err != nil {
			slog.Error("publish reportdataerr failed", "error", err)
		}
	}

	slog.Debug("published all health data", "nodeId", coll.CollectStatus().DeviceID)
}
