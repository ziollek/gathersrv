package gathersrv

import (
	"github.com/coredns/coredns/plugin"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var qualifiedRequestCount = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: plugin.Namespace,
	Subsystem: gatherSrvPluginName,
	Name:      "qualified_request_count_total",
	Help:      "Counter of qualified requests made.",
}, []string{"server"})

var unqualifiedRequestCount = promauto.NewCounterVec(prometheus.CounterOpts{
	Namespace: plugin.Namespace,
	Subsystem: gatherSrvPluginName,
	Name:      "unqualified_request_count_total",
	Help:      "Counter of unqualified requests made.",
}, []string{"server"})
