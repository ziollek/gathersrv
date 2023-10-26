package gathersrv

import (
	"github.com/coredns/coredns/plugin"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var requestCount = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: plugin.Namespace,
	Subsystem: gatherSrvPluginName,
	Name:      "request_count_total",
	Help:      "Counter of requests processed via plugin",
}, []string{"server", "qualified", "type"})

var subRequestCount = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: plugin.Namespace,
	Subsystem: gatherSrvPluginName,
	Name:      "sub_request_count_total",
	Help:      "Counter of sub requests.",
}, []string{"server", "prefix", "type", "code"})
