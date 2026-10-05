package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNetworkAccess(t *testing.T) {
	for _, value := range []string{"null", "true", "false"} {
		c, err := Parse([]byte("network_access: "+value), "c.yaml")
		require.NoError(t, err)
		if value == "null" {
			require.Nil(t, c.NetworkAccess)
		} else {
			require.Equal(t, value == "true", *c.NetworkAccess)
		}
	}
	for _, sandbox := range []string{"sandbox: read-only", "others_prs: {sandbox: read-only}", "overrides: [{match: [{prs: others}], sandbox: read-only}]"} {
		_, err := Parse([]byte("network_access: true\n"+sandbox), "c.yaml")
		require.ErrorContains(t, err, "network_access: true conflicts")
		_, err = Parse([]byte("network_access: false\n"+sandbox), "c.yaml")
		require.NoError(t, err)
	}
	c, err := Parse([]byte("overrides: [{match: [{repo: o/r}], network_access: true}]"), "c.yaml")
	require.NoError(t, err)
	require.Nil(t, c.NetworkAccess)
	require.Nil(t, c.For("other/r", true).NetworkAccess)
	require.True(t, *c.For("o/r", false).NetworkAccess)
	c, err = Parse([]byte("network_access: true\noverrides: [{match: [{prs: others}], network_access: null}]"), "c.yaml")
	require.NoError(t, err)
	require.Nil(t, c.For("o/r", false).NetworkAccess)
}
