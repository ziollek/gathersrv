package gathersrv

import (
	"fmt"
	"github.com/coredns/caddy"
	"github.com/coredns/coredns/core/dnsserver"
	"github.com/coredns/coredns/plugin"
	"strings"
)

const gatherSrvPluginName = "gathersrv"

// init registers this plugin.
func init() { plugin.Register(gatherSrvPluginName, setup) }

func setup(c *caddy.Controller) error {
	var clusters []Cluster

	c.Next() // Ignore "gathersrv" and give us the next token.
	if !c.NextArg() {
		return plugin.Error(gatherSrvPluginName, c.ArgErr())
	}
	domain := parseDomain(c.Val())
	if domain == "" {
		return plugin.Error(gatherSrvPluginName, fmt.Errorf("Provided incorrect domain <%s>", c.Val()))
	}
	for c.NextBlock() {
		suffix := parseDomain(c.Val())
		if suffix == "" {
			return plugin.Error(gatherSrvPluginName, fmt.Errorf("Provided incorrect domain <%s>", c.Val()))
		}
		if !c.NextArg() {
			return plugin.Error(gatherSrvPluginName, c.ArgErr())
		}
		prefix := c.Val()
		if c.NextArg() {
			return plugin.Error(gatherSrvPluginName, c.ArgErr())
		}
		clusters = append(clusters, Cluster{Prefix: prefix, Suffix: suffix})
	}
	if c.NextArg() {
		return plugin.Error(gatherSrvPluginName, c.ArgErr())
	}

	if len(clusters) == 0 {
		return plugin.Error(gatherSrvPluginName, fmt.Errorf("You have to provide at least one cluster definition."))
	}

	dnsserver.GetConfig(c).AddPlugin(func(next plugin.Handler) plugin.Handler {
		return GatherSrv{Clusters: clusters, Domain: domain, Next: next}
	})

	return nil
}

func parseDomain(raw string) string {
	if strings.HasSuffix(raw, ".") {
		return plugin.Name(raw).Normalize()
	}
	return ""
}
