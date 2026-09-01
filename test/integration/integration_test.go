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
    // Build the binary path
    binPath := filepath.Join("..", "..", "bin", "simon")
    if _, err := os.Stat(binPath); err != nil {
        t.Logf("Binary not found at %s, building...", binPath)
        cmd := exec.Command("make", "build")
        cmd.Dir = "../.."
        out, err := cmd.CombinedOutput()
        if err != nil {
            t.Fatalf("failed to build simon: %v\n%s", err, out)
        }
    }

    absBinPath, err := filepath.Abs(binPath)
    if err != nil {
        t.Fatalf("failed to get absolute path for binary: %v", err)
    }

    tmpDir, err := os.MkdirTemp("", "simon-test-*")
    if err != nil {
        t.Fatal(err)
    }
    defer os.RemoveAll(tmpDir)

    // Create the cluster config directory (where node YAML files live)
    clusterDir := filepath.Join(tmpDir, "test-cluster")
    if err := os.MkdirAll(clusterDir, 0755); err != nil {
        t.Fatal(err)
    }

    // Write node definition: node1.yaml
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

    // Create the cluster config file (simon/v1alpha1 Config)
    clusterConfigYAML := fmt.Sprintf(`
apiVersion: simon/v1alpha1
kind: Config
metadata:
  name: test-config
spec:
  cluster:
    customConfig: %s
    customConfig:
      shufflePod: false
  workloadTuningConfig:
    ratio: 0.9
    seed: 233
  typicalPodsConfig:
    isInvolvedCpuPods: true
    podPopularityThreshold: 95
    isConsideredGpuResWeight: false
`, clusterDir)
    clusterFile := filepath.Join(tmpDir, "cluster-config.yaml")
    if err := os.WriteFile(clusterFile, []byte(clusterConfigYAML), 0644); err != nil {
        t.Fatal(err)
    }

    // Create pod definitions (these go in the cluster config directory)
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
    podsFile := filepath.Join(clusterDir, "pods.yaml")
    if err := os.WriteFile(podsFile, []byte(podsYAML), 0644); err != nil {
        t.Fatal(err)
    }

    for _, pluginName := range plugins {
        t.Run(pluginName, func(t *testing.T) {
            // Build scheduler config with the given plugin enabled
            schedulerYAML := fmt.Sprintf(`
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
      - name: GpuClusteringScore
      - name: GpuPackingScore
      - name: BestFitScore
      - name: FGDScore
      - name: ImageLocality
      - name: NodeAffinity
      - name: PodTopologySpread
      - name: TaintToleration
      - name: NodeResourcesBalancedAllocation
      - name: InterPodAffinity
      - name: NodeResourcesLeastAllocated
      - name: NodePreferAvoidPods
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
            if err := os.WriteFile(schedFile, []byte(schedulerYAML), 0644); err != nil {
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
                t.Errorf("simon apply failed for plugin %s: %v\nOutput:\n%s", pluginName, err, output)
                return
            }

            // Verify no errors
            outStr := string(output)
            if strings.Contains(outStr, "error") || strings.Contains(outStr, "failed") {
                t.Errorf("scheduling error for plugin %s:\n%s", pluginName, outStr)
            }
        })
    }
}