//go:build linux

package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

const linuxClockTicks = 100
const maxProcessScan = 4096
const maxSystemctlOutputBytes = 2 * 1024 * 1024

var errSystemctlOutputTooLarge = errors.New("systemctl output exceeds telemetry limit")

type processCounter struct {
	startTime uint64
	cpuTicks  uint64
}

type processReading struct {
	telemetry protocol.ProcessTelemetry
	startTime uint64
	cpuTicks  uint64
}

type serviceCounter struct {
	cpuUsageNS uint64
	at         time.Time
}

type listedService struct {
	unit        string
	description string
	activeState string
	subState    string
}

type boundedCommandOutput struct {
	data     []byte
	maximum  int
	exceeded bool
}

func (output *boundedCommandOutput) Write(value []byte) (int, error) {
	remaining := output.maximum - len(output.data)
	if remaining >= len(value) {
		output.data = append(output.data, value...)
		return len(value), nil
	}
	if remaining > 0 {
		output.data = append(output.data, value[:remaining]...)
	}
	output.exceeded = true
	return max(remaining, 0), errSystemctlOutputTooLarge
}

func (collector *linuxCollector) SampleTelemetry(ctx context.Context) protocol.TelemetrySnapshotRequest {
	collector.telemetryMu.Lock()
	defer collector.telemetryMu.Unlock()

	sampleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result := protocol.TelemetrySnapshotRequest{SampledAt: timeNow().UTC()}
	processes, totalProcesses, err := collector.sampleProcesses()
	if err != nil {
		result.ProcessesErrorCode = "process_collection_failed"
	} else {
		result.ProcessesAvailable = true
		result.Processes = processes
		result.ProcessesTotal = totalProcesses
		result.ProcessesTruncated = totalProcesses > len(processes)
	}
	services, totalServices, err := collector.sampleServices(sampleCtx)
	if err != nil {
		result.ServicesErrorCode = "service_collection_failed"
	} else {
		result.ServicesAvailable = true
		result.Services = services
		result.ServicesTotal = totalServices
		result.ServicesTruncated = totalServices > len(services)
	}
	result.SampledAt = timeNow().UTC()
	return result
}

func (collector *linuxCollector) sampleProcesses() ([]protocol.ProcessTelemetry, int, error) {
	pids, totalProcesses, err := readProcessIDs(maxProcessScan)
	if err != nil {
		return nil, 0, err
	}
	totalCPU, _, err := readCPU()
	if err != nil {
		return nil, 0, err
	}
	uptime, err := readUptime()
	if err != nil {
		return nil, 0, err
	}
	readings := make([]processReading, 0, len(pids))
	current := make(map[int]processCounter, len(pids))
	for _, pid := range pids {
		reading, readErr := readProcess(pid, uptime)
		if readErr != nil {
			continue
		}
		if previous, ok := collector.previousProcesses[pid]; ok &&
			previous.startTime == reading.startTime && reading.cpuTicks >= previous.cpuTicks &&
			collector.previousProcessCPU > 0 && totalCPU > collector.previousProcessCPU {
			delta := reading.cpuTicks - previous.cpuTicks
			totalDelta := totalCPU - collector.previousProcessCPU
			reading.telemetry.CPUPercent = float64(delta) * float64(max(collector.facts.CPUCores, 1)) * 100 / float64(totalDelta)
			reading.telemetry.CPUPercent = math.Min(
				reading.telemetry.CPUPercent, float64(max(collector.facts.CPUCores, 1))*100,
			)
		}
		current[pid] = processCounter{startTime: reading.startTime, cpuTicks: reading.cpuTicks}
		readings = append(readings, reading)
	}
	collector.previousProcessCPU = totalCPU
	collector.previousProcesses = current
	return selectTopProcesses(readings, protocol.MaxTelemetryProcesses), totalProcesses, nil
}

func readProcessIDs(limit int) ([]int, int, error) {
	if limit < 1 {
		return []int{}, 0, nil
	}
	directory, err := os.Open("/proc")
	if err != nil {
		return nil, 0, fmt.Errorf("open /proc: %w", err)
	}
	defer directory.Close()
	pids := make([]int, 0, limit)
	total := 0
	for {
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			pid, parseErr := strconv.Atoi(entry.Name())
			if parseErr != nil || pid < 1 {
				continue
			}
			total++
			if len(pids) < limit {
				pids = append(pids, pid)
			}
			// One extra PID is enough to report that the bounded scan was truncated.
			if total > limit {
				return pids, total, nil
			}
		}
		if readErr == io.EOF {
			return pids, total, nil
		}
		if readErr != nil {
			return nil, 0, fmt.Errorf("read /proc: %w", readErr)
		}
	}
}

func readProcess(pid int, systemUptime int64) (processReading, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return processReading{}, err
	}
	return parseProcessStat(string(data), pid, systemUptime, readProcessUnit(pid), int64(os.Getpagesize()))
}

func parseProcessStat(value string, pid int, systemUptime int64, unit string, pageSize int64) (processReading, error) {
	open := strings.IndexByte(value, '(')
	close := strings.LastIndex(value, ")")
	if open < 0 || close <= open {
		return processReading{}, fmt.Errorf("invalid process stat")
	}
	fields := strings.Fields(value[close+1:])
	if len(fields) < 22 {
		return processReading{}, fmt.Errorf("short process stat")
	}
	userTicks, userErr := strconv.ParseUint(fields[11], 10, 64)
	systemTicks, systemErr := strconv.ParseUint(fields[12], 10, 64)
	startTime, startErr := strconv.ParseUint(fields[19], 10, 64)
	rssPages, rssErr := strconv.ParseInt(fields[21], 10, 64)
	if userErr != nil || systemErr != nil || startErr != nil || rssErr != nil {
		return processReading{}, fmt.Errorf("invalid process counters")
	}
	if rssPages < 0 {
		rssPages = 0
	}
	startedSeconds := int64(startTime / linuxClockTicks)
	processUptime := systemUptime - startedSeconds
	if processUptime < 0 {
		processUptime = 0
	}
	return processReading{
		telemetry: protocol.ProcessTelemetry{
			PID: pid, Name: boundedText(value[open+1:close], 64),
			Unit: boundedText(unit, 128), RSSBytes: rssPages * pageSize,
			UptimeSeconds: processUptime,
		},
		startTime: startTime, cpuTicks: userTicks + systemTicks,
	}, nil
}

func readProcessUnit(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		_, path, found := strings.Cut(line, ":/")
		if !found {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) != 3 {
				continue
			}
			path = strings.TrimPrefix(parts[2], "/")
		}
		segments := strings.Split(path, "/")
		for index := len(segments) - 1; index >= 0; index-- {
			if strings.HasSuffix(segments[index], ".service") {
				return boundedText(segments[index], 128)
			}
		}
	}
	return ""
}

func selectTopProcesses(readings []processReading, limit int) []protocol.ProcessTelemetry {
	if len(readings) == 0 || limit < 1 {
		return []protocol.ProcessTelemetry{}
	}
	byCPU := append([]processReading(nil), readings...)
	byMemory := append([]processReading(nil), readings...)
	sort.Slice(byCPU, func(i, j int) bool {
		if byCPU[i].telemetry.CPUPercent == byCPU[j].telemetry.CPUPercent {
			return byCPU[i].telemetry.RSSBytes > byCPU[j].telemetry.RSSBytes
		}
		return byCPU[i].telemetry.CPUPercent > byCPU[j].telemetry.CPUPercent
	})
	sort.Slice(byMemory, func(i, j int) bool {
		if byMemory[i].telemetry.RSSBytes == byMemory[j].telemetry.RSSBytes {
			return byMemory[i].telemetry.CPUPercent > byMemory[j].telemetry.CPUPercent
		}
		return byMemory[i].telemetry.RSSBytes > byMemory[j].telemetry.RSSBytes
	})
	selected := make(map[int]protocol.ProcessTelemetry, limit)
	half := limit / 2
	for _, group := range [][]processReading{byCPU[:min(half, len(byCPU))], byMemory[:min(half, len(byMemory))]} {
		for _, reading := range group {
			selected[reading.telemetry.PID] = reading.telemetry
		}
	}
	for _, reading := range byCPU {
		if len(selected) >= limit {
			break
		}
		selected[reading.telemetry.PID] = reading.telemetry
	}
	result := make([]protocol.ProcessTelemetry, 0, len(selected))
	for _, process := range selected {
		result = append(result, process)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CPUPercent == result[j].CPUPercent {
			if result[i].RSSBytes == result[j].RSSBytes {
				return result[i].PID < result[j].PID
			}
			return result[i].RSSBytes > result[j].RSSBytes
		}
		return result[i].CPUPercent > result[j].CPUPercent
	})
	return result
}

func (collector *linuxCollector) sampleServices(ctx context.Context) ([]protocol.ServiceTelemetry, int, error) {
	listOutput, err := runSystemctl(ctx, "list-units", "--type=service", "--all", "--plain", "--full", "--no-legend", "--no-pager", "--no-ask-password")
	if err != nil {
		return nil, 0, fmt.Errorf("list systemd services: %w", err)
	}
	listed, total := parseSystemdServiceList(string(listOutput))
	listed = selectSystemdServices(listed, protocol.MaxTelemetryServices)
	if len(listed) == 0 {
		collector.previousServices = make(map[string]serviceCounter)
		collector.serviceCPUPeaks = make(map[string]float64)
		return []protocol.ServiceTelemetry{}, total, nil
	}
	args := []string{"show", "--all", "--no-pager", "--no-ask-password",
		"--property=Id", "--property=Description", "--property=ActiveState", "--property=SubState",
		"--property=MainPID", "--property=NRestarts", "--property=CPUUsageNSec",
		"--property=MemoryCurrent", "--property=MemoryPeak", "--property=TasksCurrent"}
	for _, service := range listed {
		args = append(args, service.unit)
	}
	showOutput, err := runSystemctl(ctx, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("read systemd service properties: %w", err)
	}
	properties := parseSystemdProperties(string(showOutput))
	now := timeNow()
	currentCounters := make(map[string]serviceCounter, len(listed))
	currentPeaks := make(map[string]float64, len(listed))
	result := make([]protocol.ServiceTelemetry, 0, len(listed))
	for _, listedService := range listed {
		values := properties[listedService.unit]
		service := protocol.ServiceTelemetry{
			Unit: listedService.unit, Description: listedService.description,
			ActiveState: listedService.activeState, SubState: listedService.subState,
		}
		if value := boundedText(values["Description"], 256); value != "" {
			service.Description = value
		}
		if value := boundedText(values["ActiveState"], 32); value != "" {
			service.ActiveState = value
		}
		if value := boundedText(values["SubState"], 32); value != "" {
			service.SubState = value
		}
		service.MainPID = parseBoundedInt(values["MainPID"], math.MaxInt32)
		service.Restarts = int64(parseBoundedInt(values["NRestarts"], math.MaxInt32))
		service.Tasks = parseSystemdInt64(values["TasksCurrent"])
		service.MemoryBytes = parseSystemdInt64(values["MemoryCurrent"])
		service.MemoryPeakBytes = parseSystemdInt64(values["MemoryPeak"])
		if service.MemoryPeakBytes < service.MemoryBytes {
			service.MemoryPeakBytes = service.MemoryBytes
		}
		if cpuUsage, ok := parseSystemdUint(values["CPUUsageNSec"]); ok {
			if previous, found := collector.previousServices[service.Unit]; found &&
				cpuUsage >= previous.cpuUsageNS && now.After(previous.at) {
				service.CPUPercent = float64(cpuUsage-previous.cpuUsageNS) / float64(now.Sub(previous.at)) * 100
				service.CPUPercent = math.Min(service.CPUPercent, float64(max(collector.facts.CPUCores, 1))*100)
			}
			currentCounters[service.Unit] = serviceCounter{cpuUsageNS: cpuUsage, at: now}
		}
		service.CPUPeakPercent = math.Max(collector.serviceCPUPeaks[service.Unit], service.CPUPercent)
		currentPeaks[service.Unit] = service.CPUPeakPercent
		result = append(result, service)
	}
	collector.previousServices = currentCounters
	collector.serviceCPUPeaks = currentPeaks
	sort.Slice(result, func(i, j int) bool { return result[i].Unit < result[j].Unit })
	return result, total, nil
}

func runSystemctl(ctx context.Context, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "systemctl", arguments...)
	command.Env = append(command.Environ(), "LC_ALL=C", "SYSTEMD_COLORS=0")
	output := boundedCommandOutput{maximum: maxSystemctlOutputBytes}
	command.Stdout = &output
	err := command.Run()
	if output.exceeded {
		return nil, errSystemctlOutputTooLarge
	}
	if err != nil {
		return nil, err
	}
	return output.data, nil
}

func parseSystemdServiceList(output string) ([]listedService, int) {
	services := make([]listedService, 0)
	seen := make(map[string]struct{})
	total := 0
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		start := 0
		if !strings.HasSuffix(fields[0], ".service") && len(fields) >= 5 && strings.HasSuffix(fields[1], ".service") {
			start = 1
		}
		if len(fields) < start+4 || !strings.HasSuffix(fields[start], ".service") {
			continue
		}
		unit := fields[start]
		if _, ok := seen[unit]; ok {
			continue
		}
		seen[unit] = struct{}{}
		total++
		// Unit names are identifiers. Never truncate them before passing them
		// back to systemctl or reporting them to the Server.
		if len(unit) > 128 || boundedText(unit, 128) != unit {
			continue
		}
		services = append(services, listedService{
			unit: unit, activeState: boundedText(fields[start+2], 32),
			subState:    boundedText(fields[start+3], 32),
			description: boundedText(strings.Join(fields[start+4:], " "), 256),
		})
	}
	return services, total
}

func selectSystemdServices(services []listedService, limit int) []listedService {
	result := append([]listedService(nil), services...)
	sort.Slice(result, func(i, j int) bool {
		rank := func(service listedService) int {
			if service.activeState == "failed" {
				return 0
			}
			if service.activeState == "active" {
				return 1
			}
			return 2
		}
		left, right := rank(result[i]), rank(result[j])
		if left == right {
			return result[i].unit < result[j].unit
		}
		return left < right
	})
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

func parseSystemdProperties(output string) map[string]map[string]string {
	result := make(map[string]map[string]string)
	current := make(map[string]string)
	flush := func() {
		if current["Id"] != "" {
			result[current["Id"]] = current
		}
		current = make(map[string]string)
	}
	for _, line := range strings.Split(output, "\n") {
		if line == "" {
			flush()
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		if key == "Id" && current["Id"] != "" {
			flush()
		}
		current[key] = value
	}
	flush()
	return result
}

func parseSystemdUint(value string) (uint64, bool) {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return parsed, err == nil && parsed != math.MaxUint64
}

func parseSystemdInt64(value string) int64 {
	parsed, ok := parseSystemdUint(value)
	if !ok || parsed > math.MaxInt64 {
		return 0
	}
	return int64(parsed)
}

func parseBoundedInt(value string, maximum int) int {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || parsed > int64(maximum) {
		return 0
	}
	return int(parsed)
}

func boundedText(value string, maximumBytes int) string {
	value = strings.TrimSpace(value)
	var result strings.Builder
	for _, character := range value {
		if unicode.IsControl(character) {
			character = '?'
		}
		width := utf8.RuneLen(character)
		if width < 1 || result.Len()+width > maximumBytes {
			break
		}
		result.WriteRune(character)
	}
	return result.String()
}
