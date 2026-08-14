//go:build linux

package agent

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestParseProcessStatHandlesClosingParenthesisInName(t *testing.T) {
	fields := make([]string, 22)
	for index := range fields {
		fields[index] = "0"
	}
	fields[0] = "S"
	fields[11] = "20"
	fields[12] = "5"
	fields[19] = "1000"
	fields[21] = "3"
	reading, err := parseProcessStat(
		"42 (worker ) name) "+strings.Join(fields, " "), 42, 100,
		"worker.service", 4096,
	)
	if err != nil {
		t.Fatalf("parseProcessStat() error = %v", err)
	}
	if reading.telemetry.Name != "worker ) name" || reading.cpuTicks != 25 ||
		reading.telemetry.RSSBytes != 12288 || reading.telemetry.UptimeSeconds != 90 {
		t.Fatalf("process reading = %#v", reading)
	}
}

func TestSelectTopProcessesIncludesCPUAndMemoryLeaders(t *testing.T) {
	readings := make([]processReading, 20)
	for index := range readings {
		readings[index].telemetry = protocol.ProcessTelemetry{
			PID: index + 1, Name: "process-" + strconv.Itoa(index+1),
			CPUPercent: float64(20 - index), RSSBytes: int64(index+1) * 1024,
		}
	}
	selected := selectTopProcesses(readings, protocol.MaxTelemetryProcesses)
	if len(selected) != protocol.MaxTelemetryProcesses {
		t.Fatalf("selected processes = %d", len(selected))
	}
	seen := make(map[int]bool)
	for _, process := range selected {
		seen[process.PID] = true
	}
	if !seen[1] || !seen[20] {
		t.Fatalf("selection omitted CPU or memory leader: %#v", selected)
	}
}

func TestParseSystemdTelemetryOutput(t *testing.T) {
	listed, total := parseSystemdServiceList(`
alpha.service loaded active running Alpha service
* failed.service loaded failed failed Failed service
`)
	if total != 2 || len(listed) != 2 || listed[0].unit != "alpha.service" || listed[1].activeState != "failed" {
		t.Fatalf("listed services = %#v", listed)
	}
	properties := parseSystemdProperties(`Id=alpha.service
Description=Alpha service
ActiveState=active
CPUUsageNSec=1000

Id=failed.service
ActiveState=failed
CPUUsageNSec=[not set]
`)
	if properties["alpha.service"]["Description"] != "Alpha service" {
		t.Fatalf("properties = %#v", properties)
	}
	if _, ok := parseSystemdUint(properties["failed.service"]["CPUUsageNSec"]); ok {
		t.Fatal("parseSystemdUint() accepted an unavailable counter")
	}
}

func TestParseSystemdServiceListSkipsUnrepresentableUnitNames(t *testing.T) {
	longUnit := strings.Repeat("a", 129) + ".service"
	listed, total := parseSystemdServiceList(strings.Join([]string{
		longUnit + " loaded active running Overlong service",
		"normal.service loaded active running Normal service",
		longUnit + " loaded active running Duplicate overlong service",
	}, "\n"))
	if total != 2 {
		t.Fatalf("total services = %d, want 2", total)
	}
	selected := selectSystemdServices(listed, protocol.MaxTelemetryServices)
	if len(selected) != 1 || selected[0].unit != "normal.service" {
		t.Fatalf("selected services = %#v", selected)
	}
	if total <= len(selected) {
		t.Fatalf("total = %d, selected = %d; skipped service must make the snapshot truncated", total, len(selected))
	}
}

func TestBoundedCommandOutput(t *testing.T) {
	exact := boundedCommandOutput{maximum: 4}
	if written, err := exact.Write([]byte("abcd")); err != nil || written != 4 || string(exact.data) != "abcd" {
		t.Fatalf("exact-limit write = (%d, %v, %q)", written, err, exact.data)
	}

	overflow := boundedCommandOutput{maximum: 4}
	if written, err := overflow.Write([]byte("abc")); err != nil || written != 3 {
		t.Fatalf("initial write = (%d, %v)", written, err)
	}
	written, err := overflow.Write([]byte("de"))
	if written != 1 || !errors.Is(err, errSystemctlOutputTooLarge) ||
		!overflow.exceeded || string(overflow.data) != "abcd" {
		t.Fatalf("overflow write = (%d, %v, %t, %q)", written, err, overflow.exceeded, overflow.data)
	}
}
