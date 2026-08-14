package agent

import (
	"context"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type HostFacts struct {
	OS            string
	OSVersion     string
	Architecture  string
	Hostname      string
	KernelVersion string
	CPUCores      int
}

type Collector interface {
	Facts() HostFacts
	Sample(context.Context) (protocol.HostMetrics, error)
	ServiceRunning(context.Context, string) bool
}

type TelemetryCollector interface {
	SampleTelemetry(context.Context) protocol.TelemetrySnapshotRequest
}

type networkSample struct {
	rxBytes int64
	txBytes int64
	at      time.Time
}

type diskSample struct {
	readBytes  int64
	writeBytes int64
	at         time.Time
}
