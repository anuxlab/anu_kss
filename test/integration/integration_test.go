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
    "CAFGDScore",
}

func TestAllPlugins(t *testing.T) {
    // Build the binary path relative to this test file
    binPath := filepath.Join("..", "..", "bin", "simon")

    // Check if the binary exists; if not, build it
    if _, err := os.Stat(binPath); err != nil {
        t.Logf("Binary not found at %s, building...", binPath)
        cmd := exec.Command("make", "build")
        cmd.Dir = "../.." // project root
        out, err := cmd.CombinedOutput()
        if err != nil {
            t.Fatalf("failed to build simon: %v\n%s", err, out)
        }
        // Recheck after build
        if _, err := os.Stat(binPath); err != nil {
            t.Fatalf("binary still not found after build: %v", err)
        }
    }

    // Convert to absolute path so that it works regardless of working directory
    absBinPath, err := filepath.Abs(binPath)
    if err != nil {
        t.Fatalf("failed to get absolute path for binary: %v", err)
    }

    // Create temporary directory for test files
    tmpDir, err := os.MkdirTemp("", "simon-test-*")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(tmpDir)

    // Cluster config: one node with 2 GPUs
    clusterYAML := `
nodes:
- name: node1
  cpu: 8
  memory: 16Gi
  gpu: 2
`
    clusterFile := filepath.Join(tmpDir, "cluster.yaml")
    if err := os.WriteFile(clusterFile, []byte(clusterYAML), 0644); err != nil {
        t.Fatal(err)
    }

    // Pods: 3 pods, each requesting 1 GPU
    podsYAML := `
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
    if err := os.WriteFile(podsFile, []byte(podsYAML), 0644); err != nil {
        t.Fatal(err)
    }

    for _, pluginName := range plugins {
        t.Run(pluginName, func(t *testing.T) {
            schedulerYAML := fmt.Sprintf(`
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
            if err := os.WriteFile(schedFile, []byte(schedulerYAML), 0644); err != nil {
                t.Fatal(err)
            }

            // Run simon apply:
            // Flags: -f cluster config, -s scheduler config, -e gpu, then positional args: pod files
            cmd := exec.Command(
                absBinPath,
                "apply",
                "--extended-resources", "gpu",
                "-f", clusterFile,
                "-s", schedFile,
                podsFile, // positional argument – the pods file
            )
            cmd.Dir = tmpDir
            output, err := cmd.CombinedOutput()
            if err != nil {
                t.Errorf("simon apply failed for plugin %s: %v\nOutput:\n%s", pluginName, err, output)
                return
            }

            // Verify no pending/unschedulable pods by checking output for certain keywords
            outStr := string(output)
            if strings.Contains(outStr, "pending") || strings.Contains(outStr, "unschedulable") {
                t.Errorf("some pods remained pending for plugin %s:\n%s", pluginName, outStr)
            }
        })
    }
}