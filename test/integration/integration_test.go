package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Plugins that are actually registered in the simulator.
var plugins = []string{
	"FGDScore",
	"BestFitScore",
	"DotProductScore",
	"RandomScore",
	"CAFGDScore", // your plugin – must be registered
}

func TestAllPlugins(t *testing.T) {
	// Build the binary path (relative to test file)
	binPath := filepath.Join("..", "..", "bin", "simon")
	absBinPath, err := filepath.Abs(binPath)
	if err != nil {
		t.Fatalf("failed to get absolute path: %v", err)
	}

	// If binary not found, build it
	if _, err := os.Stat(absBinPath); err != nil {
		t.Logf("Building simon...")
		cmd := exec.Command("make", "build")
		cmd.Dir = filepath.Join("..", "..")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("make build failed: %v\n%s", err, out)
		}
	}

	// Create temporary directory for test files
	tmpDir, err := os.MkdirTemp("", "simon-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create cluster config directory
	clusterDir := filepath.Join(tmpDir, "test-cluster")
	if err := os.MkdirAll(clusterDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Node definition – uses "gpu" as resource name
	nodeYAML := `
apiVersion: v1
kind: Node
metadata:
  name: node1
status:
  allocatable:
    cpu: "8"
    memory: 16Gi
    gpu: "2"
`
	nodeFile := filepath.Join(clusterDir, "node1.yaml")
	if err := os.WriteFile(nodeFile, []byte(nodeYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Single pod with both requests and limits for gpu
	podYAML := `
apiVersion: v1
kind: Pod
metadata:
  name: test-pod
spec:
  containers:
  - name: app
    image: nginx
    resources:
      requests:
        cpu: "100m"
        memory: "128Mi"
        gpu: "1"
      limits:
        cpu: "100m"
        memory: "128Mi"
        gpu: "1"
`
	podFile := filepath.Join(clusterDir, "pods.yaml")
	if err := os.WriteFile(podFile, []byte(podYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Absolute path to cluster directory for config
	absClusterDir, err := filepath.Abs(clusterDir)
	if err != nil {
		t.Fatal(err)
	}

	// Cluster config (simon/v1alpha1 Config)
	clusterConfig := fmt.Sprintf(`
apiVersion: simon/v1alpha1
kind: Config
metadata:
  name: test-config
spec:
  cluster:
    customConfig: %s
  workloadTuningConfig:
    ratio: 0.9
    seed: 233
  typicalPodsConfig:
    isInvolvedCpuPods: true
    podPopularityThreshold: 95
    isConsideredGpuResWeight: false
`, absClusterDir)
	clusterFile := filepath.Join(tmpDir, "cluster-config.yaml")
	if err := os.WriteFile(clusterFile, []byte(clusterConfig), 0644); err != nil {
		t.Fatal(err)
	}

	for _, pluginName := range plugins {
		t.Run(pluginName, func(t *testing.T) {
			// Scheduler config enabling only the plugin under test
			schedYAML := fmt.Sprintf(`
apiVersion: kubescheduler.config.k8s.io/v1beta1
kind: KubeSchedulerConfiguration
percentageOfNodesToScore: 100
profiles:
- schedulerName: simon-scheduler
  plugins:
    filter:
      enabled:
      - name: Open-Gpu-Share
    score:
      disabled:
      - name: RandomScore
      - name: DotProductScore
      - name: BestFitScore
      - name: FGDScore
      enabled:
      - name: %s
        weight: 1000
    reserve:
      enabled:
      - name: Open-Gpu-Share
    bind:
      disabled:
      - name: DefaultBinder
      enabled:
      - name: Simon
  pluginConfig:
  - name: %s
    args:
      dimExtMethod: share
      normMethod: max
  - name: Open-Gpu-Share
    args:
      dimExtMethod: share
      normMethod: max
      gpuSelMethod: %s
`, pluginName, pluginName, pluginName)
			schedFile := filepath.Join(tmpDir, fmt.Sprintf("scheduler-%s.yaml", pluginName))
			if err := os.WriteFile(schedFile, []byte(schedYAML), 0644); err != nil {
				t.Fatal(err)
			}

			// Run simon apply
			cmd := exec.Command(
				absBinPath,
				"apply",
				"--extended-resources", "gpu",
				"-f", clusterFile,
				"-s", schedFile,
			)
			cmd.Dir = tmpDir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("simon apply failed for %s: %v\nOutput:\n%s", pluginName, err, output)
				return
			}
			// Check for errors in output
			if strings.Contains(string(output), "error") || strings.Contains(string(output), "failed") {
				t.Errorf("Scheduling error for %s:\n%s", pluginName, output)
			}
		})
	}
}