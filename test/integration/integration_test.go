package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/apply"
	"github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/simontype"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
)

// TestAPI tests the scheduling for all plugins using the simulator API directly.
func TestAPI(t *testing.T) {
	// Build the list of plugins to test.
	plugins := []string{
		"FGDScore",
		"BestFitScore",
		"DotProductScore",
		"RandomScore",
		"CAFGDScore",
	}

	// Create a temporary directory for config files.
	tmpDir, err := os.MkdirTemp("", "simon-api-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a cluster directory with node and pod files.
	clusterDir := filepath.Join(tmpDir, "test-cluster")
	if err := os.MkdirAll(clusterDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Node definition – use "gpu" as the resource name.
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
	nodeFile := filepath.Join(clusterDir, "node1.yaml")
	if err := writeYAML(nodeFile, node); err != nil {
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
	podFile := filepath.Join(clusterDir, "pods.yaml")
	if err := writeYAML(podFile, pod); err != nil {
		t.Fatal(err)
	}

	// Cluster config.
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
	clusterFile := filepath.Join(tmpDir, "cluster-config.yaml")
	if err := writeYAML(clusterFile, clusterConfig); err != nil {
		t.Fatal(err)
	}

	for _, pluginName := range plugins {
		t.Run(pluginName, func(t *testing.T) {
			// Create a scheduler config for this plugin.
			schedConfig := &simontype.KubeSchedulerConfig{
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
								Args: runtime.RawExtension{
									Raw: []byte(`{"dimExtMethod":"share","normMethod":"max"}`),
								},
							},
							{
								Name: "Open-Gpu-Share",
								Args: runtime.RawExtension{
									Raw: []byte(fmt.Sprintf(`{"dimExtMethod":"share","normMethod":"max","gpuSelMethod":"%s"}`, pluginName)),
								},
							},
						},
					},
				},
			}
			schedFile := filepath.Join(tmpDir, fmt.Sprintf("scheduler-%s.yaml", pluginName))
			if err := writeYAML(schedFile, schedConfig); err != nil {
				t.Fatal(err)
			}

			// Load the configurations using the apply package helpers.
			cluster, err := apply.LoadClusterConfig(clusterFile)
			if err != nil {
				t.Fatalf("failed to load cluster config: %v", err)
			}
			scheduler, err := apply.LoadSchedulerConfig(schedFile)
			if err != nil {
				t.Fatalf("failed to load scheduler config: %v", err)
			}

			// Create the simulator.
			sim, err := simulator.NewSimulator(cluster, scheduler, nil)
			if err != nil {
				t.Fatalf("failed to create simulator: %v", err)
			}

			// Run the simulation.
			if err := sim.Run(); err != nil {
				t.Fatalf("simulation failed: %v", err)
			}

			// Check that all pods are scheduled.
			unscheduled := sim.GetUnscheduledPods()
			if len(unscheduled) > 0 {
				t.Errorf("some pods remained unscheduled: %v", unscheduled)
			}
		})
	}
}

// Helper to write a YAML file.
func writeYAML(filename string, obj runtime.Object) error {
	data, err := runtime.Encode(scheme.Codecs.LegacyCodec(scheme.Scheme.PrioritizedVersionsAllGroups()...), obj)
	if err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0644)
}