package integration

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/simulator"
	"github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/simontype"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"
)

func TestAllPluginsAPI(t *testing.T) {
	plugins := []string{
		"FGDScore",
		"BestFitScore",
		"DotProductScore",
		"RandomScore",
		"CAFGDScore",
	}

	for _, pluginName := range plugins {
		t.Run(pluginName, func(t *testing.T) {
			// Create a temporary directory for the cluster config.
			tmpDir, err := os.MkdirTemp("", "simon-api-*")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(tmpDir)

			clusterDir := filepath.Join(tmpDir, "test-cluster")
			if err := os.MkdirAll(clusterDir, 0755); err != nil {
				t.Fatal(err)
			}

			// Node definition – use "gpu" as the extended resource.
			node := &v1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "node1"},
				Status: v1.NodeStatus{
					Capacity: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("8"),
						v1.ResourceMemory: resource.MustParse("16Gi"),
						v1.ResourceName("gpu"): resource.MustParse("2"),
					},
					Allocatable: v1.ResourceList{
						v1.ResourceCPU:    resource.MustParse("8"),
						v1.ResourceMemory: resource.MustParse("16Gi"),
						v1.ResourceName("gpu"): resource.MustParse("2"),
					},
				},
			}
			nodeData, err := yaml.Marshal(node)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(clusterDir, "node1.yaml"), nodeData, 0644); err != nil {
				t.Fatal(err)
			}

			// Pod definition – request "gpu".
			pod := &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
				Spec: v1.PodSpec{
					Containers: []v1.Container{
						{
							Name:  "app",
							Image: "nginx",
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceCPU:    resource.MustParse("100m"),
									v1.ResourceMemory: resource.MustParse("128Mi"),
									v1.ResourceName("gpu"): resource.MustParse("1"),
								},
								Limits: v1.ResourceList{
									v1.ResourceCPU:    resource.MustParse("100m"),
									v1.ResourceMemory: resource.MustParse("128Mi"),
									v1.ResourceName("gpu"): resource.MustParse("1"),
								},
							},
						},
					},
				},
			}
			podData, err := yaml.Marshal(pod)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(clusterDir, "pods.yaml"), podData, 0644); err != nil {
				t.Fatal(err)
			}

			// Build cluster config.
			clusterConfig := &simontype.ClusterConfig{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "simon/v1alpha1",
					Kind:       "Config",
				},
				ObjectMeta: metav1.ObjectMeta{Name: "test-config"},
				Spec: simontype.ClusterConfigSpec{
					Cluster: simontype.ClusterSpec{
						CustomCluster: clusterDir,
					},
					WorkloadTuningConfig: simontype.WorkloadTuningConfig{
						Ratio: 0.9,
						Seed:  233,
					},
					TypicalPodsConfig: simontype.TypicalPodsConfig{
						IsInvolvedCpuPods:      true,
						PodPopularityThreshold: 95,
						IsConsideredGpuResWeight: false,
					},
				},
			}

			// Build scheduler config.
			pluginConfigArgs := map[string]interface{}{
				"dimExtMethod": "share",
				"normMethod":   "max",
			}
			pluginArgsJSON, err := json.Marshal(pluginConfigArgs)
			if err != nil {
				t.Fatal(err)
			}
			openGpuShareArgs := map[string]interface{}{
				"dimExtMethod": "share",
				"normMethod":   "max",
				"gpuSelMethod": pluginName,
			}
			openGpuShareJSON, err := json.Marshal(openGpuShareArgs)
			if err != nil {
				t.Fatal(err)
			}

			schedulerConfig := &simontype.KubeSchedulerConfiguration{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "kubescheduler.config.k8s.io/v1beta1",
					Kind:       "KubeSchedulerConfiguration",
				},
				PercentageOfNodesToScore: 100,
				Profiles: []simontype.Profile{
					{
						SchedulerName: "simon-scheduler",
						Plugins: simontype.Plugins{
							Filter: simontype.PluginSet{
								Enabled: []simontype.Plugin{{Name: "Open-Gpu-Share"}},
							},
							Score: simontype.PluginSet{
								Disabled: []simontype.Plugin{
									{Name: "RandomScore"},
									{Name: "DotProductScore"},
									{Name: "BestFitScore"},
									{Name: "FGDScore"},
								},
								Enabled: []simontype.Plugin{
									{Name: pluginName, Weight: 1000},
								},
							},
							Reserve: simontype.PluginSet{
								Enabled: []simontype.Plugin{{Name: "Open-Gpu-Share"}},
							},
							Bind: simontype.PluginSet{
								Disabled: []simontype.Plugin{{Name: "DefaultBinder"}},
								Enabled:  []simontype.Plugin{{Name: "Simon"}},
							},
						},
						PluginConfig: []simontype.PluginConfig{
							{
								Name: pluginName,
								Args: runtime.RawExtension{Raw: pluginArgsJSON},
							},
							{
								Name: "Open-Gpu-Share",
								Args: runtime.RawExtension{Raw: openGpuShareJSON},
							},
						},
					},
				},
			}

			// Create the simulator.
			sim, err := simulator.NewSimulator(clusterConfig, schedulerConfig, nil)
			if err != nil {
				t.Fatalf("failed to create simulator: %v", err)
			}

			// Run the simulation.
			if err := sim.Run(); err != nil {
				t.Fatalf("simulation failed: %v", err)
			}

			// Check for unscheduled pods.
			unscheduled := sim.GetUnscheduledPods()
			if len(unscheduled) > 0 {
				t.Errorf("some pods remained unscheduled: %v", unscheduled)
			}
		})
	}
}