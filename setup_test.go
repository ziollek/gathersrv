package gathersrv

import (
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/coredns/caddy"
)

func TestShouldFailIfPassedIncorrectDomain(t *testing.T) {
	c := caddy.NewTestController("dns", `gathersrv distro { }`)
	err := setup(c)
	require.Errorf(t, err, "Expected error if distributed domain is incorrect")
	require.Contains(t, err.Error(), "Provided incorrect domain <distro>")
}

func TestShouldFailIfNoClustersSpecified(t *testing.T) {
	c := caddy.NewTestController("dns", `gathersrv distro.local. { }`)
	err := setup(c)
	require.Errorf(t, err, "Expected error if no cluster specified")
	require.Contains(t, err.Error(), "You have to provide at least one cluster definition.")
}

func TestShouldFailIfPassedIncorrectClusterDomain(t *testing.T) {
	config := `gathersrv distro.local. {
	cluster-a a-
}`
	c := caddy.NewTestController("dns", config)
	err := setup(c)
	require.Errorf(t, err, "Expected error if cluster domain is incorrect")
	require.Contains(t, err.Error(), "Provided incorrect domain <cluster-a>")
}

func TestShouldSetupProperly(t *testing.T) {
	config := `gathersrv distro.local. {
	cluster-a.local. a-
}`
	c := caddy.NewTestController("dns", config)
	err := setup(c)
	require.NoError(t, err)
}
