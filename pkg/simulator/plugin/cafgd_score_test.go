package plugin

import (
	"testing"

	simontype "github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/type"
)

func TestCAFGDScorePlugin_Name(t *testing.T) {
	plugin := &CAFGDScorePlugin{}
	if plugin.Name() != CAFGDScorePluginName {
		t.Errorf("expected %s, got %s", CAFGDScorePluginName, plugin.Name())
	}
}

// You can add more tests for helper functions later.
// For now, this at least tests the plugin is defined and its Name works.