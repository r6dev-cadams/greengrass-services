package collector

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/r6dev-cadams/greengrass-services/psu-monitor/config"
)

// ──── MQTT Payload Models ────────────────────────────────────────────────

// PSUStatus represents the status of a single PSU.
type PSUStatus struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Status  string `json:"status"`  // "OK", "Failed", "Missing", "Unknown"
	Health  string `json:"health"`  // "OK", "Critical", "Warning", "Unknown"
	Model   string `json:"model"`
	Watts   int    `json:"watts"`
}

// PSUPayload is published to greengrass/device/psu-status.
type PSUPayload struct {
	DeviceID      string    `json:"device_id"`
	ServerID      string    `json:"server_id"`
	ServerModel   string    `json:"server_model"`
	ServiceTag    string    `json:"service_tag"`
	Timestamp     string    `json:"timestamp"`
	PSU1          PSUStatus `json:"psu1"`
	PSU2          PSUStatus `json:"psu2"`
	OverallStatus string    `json:"overall_status"` // "OK", "PSU1_FAILED", "PSU2_FAILED", "BOTH_FAILED"
}

// ReportCenterPayload mirrors the health-check format for resolution center.
type ReportCenterPayload struct {
	DeviceID          string `json:"deviceId"`
	ReportStatus      string `json:"reportStatus"`
	ReportStatusColor string `json:"reportStatusColor"`
}

// ──── Redfish Response Models ────────────────────────────────────────────

// redfishPower is a subset of the Redfish Power endpoint response.
type redfishPower struct {
	PowerSupplies []redfishPSU `json:"PowerSupplies"`
}

type redfishPSU struct {
	MemberID             string       `json:"MemberId"`
	Name                 string       `json:"Name"`
	Model                string       `json:"Model"`
	PowerCapacityWatts   int          `json:"PowerCapacityWatts"`
	LastPowerOutputWatts int          `json:"LastPowerOutputWatts"`
	Status               redfishState `json:"Status"`
}

type redfishState struct {
	State  string `json:"State"`  // "Enabled", "Absent", "StandbyOffline"
	Health string `json:"Health"` // "OK", "Critical", "Warning"
}

// ──── PSU Collector ──────────────────────────────────────────────────────

// Collector polls the iDRAC Redfish API for PSU status.
type Collector struct {
	cfg        *config.Config
	httpClient *http.Client
	baseURL    string
	serviceTag string
}

// New creates a PSU collector.
func New(cfg *config.Config) *Collector {
	return &Collector{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true, // iDRAC uses self-signed certs
				},
				MaxIdleConns:        2,
				IdleConnTimeout:     30 * time.Second,
				DisableKeepAlives:   false,
			},
		},
		baseURL: fmt.Sprintf("https://%s:%d", cfg.IDRACHost, cfg.IDRACPort),
	}
}

// FetchServiceTag retrieves the iDRAC service tag (called once at startup).
func (c *Collector) FetchServiceTag() string {
	if c.serviceTag != "" {
		return c.serviceTag
	}

	body, err := c.redfishGet("/redfish/v1/")
	if err != nil {
		slog.Warn("failed to get service tag", "error", err)
		return "unknown"
	}

	var root struct {
		Oem struct {
			Dell struct {
				ServiceTag string `json:"ServiceTag"`
			} `json:"Dell"`
		} `json:"Oem"`
	}
	if err := json.Unmarshal(body, &root); err == nil && root.Oem.Dell.ServiceTag != "" {
		c.serviceTag = root.Oem.Dell.ServiceTag
		return c.serviceTag
	}

	// Fallback: try Systems endpoint
	body, err = c.redfishGet("/redfish/v1/Systems/System.Embedded.1")
	if err != nil {
		return "unknown"
	}
	var sys struct {
		SKU string `json:"SKU"`
	}
	if err := json.Unmarshal(body, &sys); err == nil && sys.SKU != "" {
		c.serviceTag = sys.SKU
		return c.serviceTag
	}

	return "unknown"
}

// CollectPSU fetches PSU status from the iDRAC Redfish Power endpoint.
func (c *Collector) CollectPSU() *PSUPayload {
	payload := &PSUPayload{
		DeviceID:    c.cfg.NodeID,
		ServerID:    c.cfg.ServerID,
		ServerModel: "Dell R420",
		ServiceTag:  c.FetchServiceTag(),
		Timestamp:   time.Now().Format(time.RFC3339),
		PSU1: PSUStatus{
			Name:   "PSU 1",
			Status: "Unknown",
			Health: "Unknown",
		},
		PSU2: PSUStatus{
			Name:   "PSU 2",
			Status: "Unknown",
			Health: "Unknown",
		},
		OverallStatus: "Unknown",
	}

	body, err := c.redfishGet("/redfish/v1/Chassis/System.Embedded.1/Power")
	if err != nil {
		slog.Error("redfish power query failed", "error", err)
		payload.OverallStatus = "IDRAC_UNREACHABLE"
		return payload
	}

	var power redfishPower
	if err := json.Unmarshal(body, &power); err != nil {
		slog.Error("redfish power parse failed", "error", err)
		payload.OverallStatus = "PARSE_ERROR"
		return payload
	}

	// Map Redfish PSUs to our payload
	for i, psu := range power.PowerSupplies {
		status := mapPSUStatus(&psu)
		if i == 0 {
			payload.PSU1 = status
			payload.PSU1.Name = "PSU 1"
		} else if i == 1 {
			payload.PSU2 = status
			payload.PSU2.Name = "PSU 2"
		}
	}

	// If fewer than 2 PSUs returned, the missing one is absent
	if len(power.PowerSupplies) < 2 {
		if len(power.PowerSupplies) < 1 {
			payload.PSU1 = PSUStatus{Name: "PSU 1", Status: "Missing", Health: "Critical"}
		}
		payload.PSU2 = PSUStatus{Name: "PSU 2", Status: "Missing", Health: "Critical"}
	}

	// Determine overall status
	psu1Bad := payload.PSU1.Health == "Critical" || !payload.PSU1.Present || payload.PSU1.Status == "Missing"
	psu2Bad := payload.PSU2.Health == "Critical" || !payload.PSU2.Present || payload.PSU2.Status == "Missing"

	switch {
	case psu1Bad && psu2Bad:
		payload.OverallStatus = "BOTH_FAILED"
	case psu1Bad:
		payload.OverallStatus = "PSU1_FAILED"
	case psu2Bad:
		payload.OverallStatus = "PSU2_FAILED"
	default:
		payload.OverallStatus = "OK"
	}

	return payload
}

// ReportCenterFromPSU generates a report center payload based on PSU status.
func (c *Collector) ReportCenterFromPSU(psu *PSUPayload) *ReportCenterPayload {
	switch psu.OverallStatus {
	case "OK":
		return &ReportCenterPayload{
			DeviceID:          c.cfg.NodeID,
			ReportStatus:      "NoReport",
			ReportStatusColor: "green",
		}
	case "PSU1_FAILED", "PSU2_FAILED":
		return &ReportCenterPayload{
			DeviceID:          c.cfg.NodeID,
			ReportStatus:      "Report",
			ReportStatusColor: "red",
		}
	default: // BOTH_FAILED, IDRAC_UNREACHABLE, etc.
		return &ReportCenterPayload{
			DeviceID:          c.cfg.NodeID,
			ReportStatus:      "Report",
			ReportStatusColor: "red",
		}
	}
}

// ──── Helpers ────────────────────────────────────────────────────────────

func mapPSUStatus(psu *redfishPSU) PSUStatus {
	s := PSUStatus{
		Model: psu.Model,
		Watts: psu.PowerCapacityWatts,
	}

	state := strings.ToLower(psu.Status.State)
	health := strings.ToLower(psu.Status.Health)

	switch {
	case state == "enabled" && (health == "ok" || health == ""):
		s.Present = true
		s.Status = "OK"
		s.Health = "OK"
	case state == "enabled" && health == "warning":
		s.Present = true
		s.Status = "Degraded"
		s.Health = "Warning"
	case state == "enabled" && health == "critical":
		s.Present = true
		s.Status = "Failed"
		s.Health = "Critical"
	case state == "absent" || state == "":
		s.Present = false
		s.Status = "Missing"
		s.Health = "Critical"
	case state == "standbyoffline":
		s.Present = true
		s.Status = "Standby"
		s.Health = psu.Status.Health
	default:
		s.Present = state != "absent"
		s.Status = psu.Status.State
		s.Health = psu.Status.Health
		if s.Health == "" {
			s.Health = "Unknown"
		}
	}

	return s
}

func (c *Collector) redfishGet(path string) ([]byte, error) {
	url := c.baseURL + path
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.cfg.IDRACUsername, c.cfg.IDRACPassword)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	return body, nil
}
