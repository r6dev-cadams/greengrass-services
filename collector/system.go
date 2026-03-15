package collector

import (
	"bufio"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"syscall"
)

// ──── MQTT Payload Models ──────────────────────────────────────────────────

// StatusPayload is published to greengrass/device/status.
type StatusPayload struct {
	DeviceID string `json:"device_id"`
	State    string `json:"state"`
}

// ReportCenterPayload is published to greengrass/device/reportcenter.
type ReportCenterPayload struct {
	DeviceID          string `json:"deviceId"`
	ReportStatus      string `json:"reportStatus"`
	ReportStatusColor string `json:"reportStatusColor"`
}

// WifiAddrPayload is published to greengrass/device/wifiaddr.
type WifiAddrPayload struct {
	DeviceID string `json:"device_id"`
	WifiAddr string `json:"wifiaddr"`
}

// SysinfoPayload is published to greengrass/device/sysinfo.
type SysinfoPayload struct {
	DeviceID             string               `json:"device_id"`
	OSVersion            string               `json:"os_version"`
	SerialNumber         string               `json:"serial_number"`
	Hardware             string               `json:"hardware"`
	Revision             string               `json:"revision"`
	SysTime              string               `json:"sys_time"`
	RootFilesystemDetails RootFilesystemDetails `json:"root_filesystem_details"`
	TotalMemory          string               `json:"total_memory"`
	AvailableMemory      string               `json:"available_memory"`
	BluetoothMacAddress  string               `json:"bluetooth_mac_address"`
	CPUTemp              string               `json:"cpu_temp"`
	MyMesh               string               `json:"my_mesh"`
	MeshNeighbors        []string             `json:"mesh_neighbors"`
}

// RootFilesystemDetails contains disk usage for the root partition.
type RootFilesystemDetails struct {
	Partition   string  `json:"partition"`
	TotalGB     float64 `json:"total_gb"`
	UsedPercent float64 `json:"used_percent"`
	UsedGB      float64 `json:"used_gb"`
	AvailableGB float64 `json:"available_gb"`
}

// ──── System Collector ──────────────────────────────────────────────────────

// Collector gathers real system information from the host OS (Linux/Raspberry Pi).
type Collector struct {
	nodeID string
}

// New creates a new system info collector.
func New(nodeID string) *Collector {
	return &Collector{nodeID: nodeID}
}

// CollectStatus returns the device health status.
// Currently always Healthy; extend with real health checks as needed.
func (c *Collector) CollectStatus() *StatusPayload {
	return &StatusPayload{
		DeviceID: c.nodeID,
		State:    "Healthy",
	}
}

// CollectReportCenter returns report center status.
func (c *Collector) CollectReportCenter() *ReportCenterPayload {
	return &ReportCenterPayload{
		DeviceID:          c.nodeID,
		ReportStatus:      "NoReport",
		ReportStatusColor: "green",
	}
}

// CollectWifiAddr returns the primary non-loopback IP address.
func (c *Collector) CollectWifiAddr() *WifiAddrPayload {
	return &WifiAddrPayload{
		DeviceID: c.nodeID,
		WifiAddr: getPrimaryIP(),
	}
}

// CollectSysinfo gathers full system information.
func (c *Collector) CollectSysinfo() *SysinfoPayload {
	totalMem, availMem := getMemoryGB()
	meshIface, meshNeighbors := getMeshInfo()

	return &SysinfoPayload{
		DeviceID:             c.nodeID,
		OSVersion:            getOSVersion(),
		SerialNumber:         getCPUSerial(),
		Hardware:             getCPUHardware(),
		Revision:             getCPURevision(),
		SysTime:              time.Now().Format("2006-01-02 15:04:05 MST-0700"),
		RootFilesystemDetails: getDiskUsage("/"),
		TotalMemory:          fmt.Sprintf("%.2f", totalMem),
		AvailableMemory:      fmt.Sprintf("%.2f", availMem),
		BluetoothMacAddress:  getBluetoothMAC(),
		CPUTemp:              getCPUTemp(),
		MyMesh:               meshIface,
		MeshNeighbors:        meshNeighbors,
	}
}

// ──── System Data Helpers ───────────────────────────────────────────────────

// getOSVersion reads /etc/os-release for PRETTY_NAME.
func getOSVersion() string {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return "unknown"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			val := strings.TrimPrefix(line, "PRETTY_NAME=")
			return strings.Trim(val, "\"")
		}
	}
	return "unknown"
}

// getCPUSerial reads the serial number from /proc/cpuinfo (Raspberry Pi).
func getCPUSerial() string {
	return parseCPUInfo("Serial")
}

// getCPUHardware reads the hardware field from /proc/cpuinfo.
func getCPUHardware() string {
	return parseCPUInfo("Hardware")
}

// getCPURevision reads the revision from /proc/cpuinfo.
func getCPURevision() string {
	return parseCPUInfo("Revision")
}

// parseCPUInfo extracts a field from /proc/cpuinfo.
func parseCPUInfo(field string) string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "unknown"
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, field) {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

// getCPUTemp reads the CPU thermal zone temperature.
func getCPUTemp() string {
	data, err := os.ReadFile("/sys/class/thermal/thermal_zone0/temp")
	if err != nil {
		return "+0.0"
	}
	raw := strings.TrimSpace(string(data))
	// Value is in millidegrees Celsius
	var milliC int
	fmt.Sscanf(raw, "%d", &milliC)
	return fmt.Sprintf("+%.1f", float64(milliC)/1000.0)
}

// getMemoryGB reads /proc/meminfo and returns (totalGB, availableGB).
func getMemoryGB() (float64, float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()

	var totalKB, availKB uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "MemTotal:"):
			fmt.Sscanf(line, "MemTotal: %d kB", &totalKB)
		case strings.HasPrefix(line, "MemAvailable:"):
			fmt.Sscanf(line, "MemAvailable: %d kB", &availKB)
		}
	}
	return float64(totalKB) / (1024 * 1024), float64(availKB) / (1024 * 1024)
}

// getDiskUsage returns root filesystem details using statfs.
func getDiskUsage(path string) RootFilesystemDetails {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		slog.Warn("statfs failed", "path", path, "error", err)
		return RootFilesystemDetails{Partition: path}
	}

	totalBytes := stat.Blocks * uint64(stat.Bsize)
	availBytes := stat.Bavail * uint64(stat.Bsize)
	usedBytes := totalBytes - (stat.Bfree * uint64(stat.Bsize))

	totalGB := float64(totalBytes) / (1024 * 1024 * 1024)
	usedGB := float64(usedBytes) / (1024 * 1024 * 1024)
	availGB := float64(availBytes) / (1024 * 1024 * 1024)
	usedPct := 0.0
	if totalGB > 0 {
		usedPct = (usedGB / totalGB) * 100
	}

	return RootFilesystemDetails{
		Partition:   path,
		TotalGB:     round2(totalGB),
		UsedPercent: round1(usedPct),
		UsedGB:      round2(usedGB),
		AvailableGB: round2(availGB),
	}
}

// getPrimaryIP returns the first non-loopback IPv4 address.
func getPrimaryIP() string {
	// Try wlan0 first, then eth0, then any interface
	for _, iface := range []string{"wlan0", "eth0", ""} {
		if iface != "" {
			if ip := getIfaceIP(iface); ip != "" {
				return ip
			}
		}
	}

	// Fallback: any non-loopback address
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "0.0.0.0"
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return "0.0.0.0"
}

// getIfaceIP returns the IPv4 address for a named network interface.
func getIfaceIP(name string) string {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil {
			return ipNet.IP.String()
		}
	}
	return ""
}

// getBluetoothMAC reads the Bluetooth MAC from hci0.
func getBluetoothMAC() string {
	data, err := os.ReadFile("/sys/class/bluetooth/hci0/address")
	if err != nil {
		return "00:00:00:00:00:00"
	}
	return strings.TrimSpace(string(data))
}

// getMeshInfo reads batman-adv mesh interface and neighbor info.
func getMeshInfo() (string, []string) {
	// Read bat0 MAC address
	meshIface := ""
	data, err := os.ReadFile("/sys/class/net/bat0/address")
	if err == nil {
		meshIface = "bat0/" + strings.TrimSpace(string(data))
	}

	// Read mesh neighbors via batctl
	neighbors := []string{}
	out, err := exec.Command("batctl", "n", "-H").Output()
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(out)))
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			// batctl n -H format: IF Neighbor last-seen
			if len(fields) >= 2 {
				neighbors = append(neighbors, fields[1])
			}
		}
	}

	return meshIface, neighbors
}

// ──── Utility ───────────────────────────────────────────────────────────────

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}
