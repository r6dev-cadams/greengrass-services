package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/r6dev-cadams/greengrass-services/psu-monitor/collector"
	"github.com/r6dev-cadams/greengrass-services/psu-monitor/config"
	"github.com/r6dev-cadams/greengrass-services/psu-monitor/mqtt"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	// ---- 1. Structured logger ----
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))

	slog.Info("psu-monitor service starting",
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
		"serverId", cfg.ServerID,
		"idracHost", cfg.IDRACHost,
		"idracPort", cfg.IDRACPort,
		"mqttBroker", cfg.MQTTBroker,
		"pollInterval", cfg.PollInterval)

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

	// ---- 5. Initialize PSU collector ----
	coll := collector.New(cfg)

	// Fetch service tag once at startup
	serviceTag := coll.FetchServiceTag()
	slog.Info("iDRAC service tag", "serviceTag", serviceTag)

	// ---- 6. Start publish loop ----
	go publishLoop(ctx, cfg, coll, mqttClient)

	// ---- 7. Wait for shutdown signal ----
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	sig := <-sigChan
	slog.Info("shutdown signal received", "signal", sig)

	cancel()
	time.Sleep(1 * time.Second)
	slog.Info("psu-monitor service stopped", "nodeId", cfg.NodeID)
}

// publishLoop runs the periodic PSU status publish cycle.
func publishLoop(ctx context.Context, cfg *config.Config,
	coll *collector.Collector, mqttClient *mqtt.Client) {

	slog.Info("publish loop started",
		"interval", cfg.PollInterval,
		"nodeId", cfg.NodeID)

	// Immediate first publish
	publishPSU(coll, mqttClient)

	ticker := time.NewTicker(cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("publish loop stopped")
			return
		case <-ticker.C:
			publishPSU(coll, mqttClient)
		}
	}
}

// publishPSU collects PSU status and publishes to MQTT topics.
func publishPSU(coll *collector.Collector, mqttClient *mqtt.Client) {
	// 1) Collect PSU status from iDRAC Redfish
	psuPayload := coll.CollectPSU()

	// 2) Publish PSU status
	if err := mqttClient.PublishJSON("greengrass/device/psu-status", psuPayload); err != nil {
		slog.Error("publish psu-status failed", "error", err)
	}

	// 3) Publish report center status (turns Node10b red in Resolution Center on failure)
	reportPayload := coll.ReportCenterFromPSU(psuPayload)
	if err := mqttClient.PublishJSON("greengrass/device/reportcenter", reportPayload); err != nil {
		slog.Error("publish reportcenter failed", "error", err)
	}

	slog.Debug("published psu data",
		"overallStatus", psuPayload.OverallStatus,
		"psu1Health", psuPayload.PSU1.Health,
		"psu2Health", psuPayload.PSU2.Health,
		"serviceTag", psuPayload.ServiceTag)
}
