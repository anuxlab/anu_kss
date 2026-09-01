package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// All plugin names as defined in pkg/type/const.go
var plugins = []string{
	"FGDScore",
	"BestFitScore",
	"DotProductScore",
	"GPUPackingScore",
	"GPUClusteringScore",
	"RandomScore",
	"CAFGDScore", // your new plugin
}

// TestAllPlugins runs a simulation for each plugin and verifies success.
func TestAllPlugins(t *testing.T) {
	// Build the simon binary if not present
	_, err := os.Stat("../../bin/simon")
	if err != nil {
		cmd := exec.Command("make", "build")
		cmd.Dir = "../.."
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("failed to build simon: %v\n%s", err, out)
		}
	}

	// Create temporary directory for output
	tmpDir, err := os.MkdirTemp("", "simon-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Cluster config (single node with 2 GPUs)
	clusterConfig := `
nodes:
- name: node1
  cpu: 8
  memory: 16Gi
  gpu: 2
`

	clusterFile := filepath.Join(tmpDir, "cluster.yaml")
	if err := os.WriteFile(clusterFile, []byte(clusterConfig), 0644); err != nil {
		t.Fatal(err)
	}

	// Pods to schedule: 3 pods each requesting 1 GPU
	pods := `
apiVersion: v1
kind: Pod
metadata:
  name: pod1
spec:
  containers:
  - name: app
    image: nginx
    resources:
      requests:
        gpu: "1"
---
apiVersion: v1
kind: Pod
metadata:
  name: pod2
spec:
  containers:
  - name: app
    image: nginx
    resources:
      requests:
        gpu: "1"
---
apiVersion: v1
kind: Pod
metadata:
  name: pod3
spec:
  containers:
  - name: app
    image: nginx
    resources:
      requests:
        gpu: "1"
`
	podsFile := filepath.Join(tmpDir, "pods.yaml")
	if err := os.WriteFile(podsFile, []byte(pods), 0644); err != nil {
		t.Fatal(err)
	}

	for _, pluginName := range plugins {
		t.Run(pluginName, func(t *testing.T) {
			// Build scheduler config for this plugin
			schedulerConfig := fmt.Sprintf(`
apiVersion: kubescheduler.config.k8s.io/v1
kind: KubeSchedulerConfiguration
profiles:
- schedulerName: default-scheduler
  plugins:
    score:
      enabled:
      - name: %s
`, pluginName)

			schedFile := filepath.Join(tmpDir, fmt.Sprintf("scheduler-%s.yaml", pluginName))
			if err := os.WriteFile(schedFile, []byte(schedulerConfig), 0644); err != nil {
				t.Fatal(err)
			}

			// Run simon apply
			cmd := exec.Command(
				"../../bin/simon",
				"apply",
				"--extended-resources", "gpu",
				"-f", clusterFile,
				"-s", schedFile,
				"-p", podsFile,
				"-o", tmpDir, // output directory for logs
			)
			cmd.Dir = tmpDir
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Errorf("simon apply failed: %v\nOutput:\n%s", err, output)
				return
			}

			// Verify that all pods are scheduled
			// The scheduler writes a summary or we can parse the output
			// For simplicity, check that there is no "pending" or "unschedulable" in logs
			if strings.Contains(string(output), "pending") || strings.Contains(string(output), "unschedulable") {
				t.Errorf("some pods remained pending:\n%s", output)
			}

			// Optionally, parse the final cluster state JSON if it's written
			// For now, just verify exit code 0 and no error messages.
		})
	}
}