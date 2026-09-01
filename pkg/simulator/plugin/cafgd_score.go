// pkg/simulator/plugin/cafgd_score.go
package plugin

import (
    "fmt"
    "math"

    "github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/simulator"
    corev1 "k8s.io/api/core/v1"
)

const (
    // CAFGDPluginName is the name of the CAFGD plugin
    CAFGDPluginName = "CAFGD"
)

// CAFGDScorePlugin implements the Cluster-Aware Fragmentation Gradient Descent algorithm
type CAFGDScorePlugin struct {
    // Configuration parameters
    timeHorizon    int64   // Look-ahead window for time-aware decisions (in seconds)
    alpha          float64 // Weight for fragmentation vs other objectives (0-1)
    hierarchical   bool    // Enable hierarchical fragmentation calculation
    gpuResourceName string // Extended resource name for GPUs
}

// NewCAFGDScorePlugin creates a new CAFGD plugin instance
func NewCAFGDScorePlugin(config map[string]interface{}) *CAFGDScorePlugin {
    plugin := &CAFGDScorePlugin{
        timeHorizon:     3600,  // default: 1 hour
        alpha:           0.7,   // default weight
        hierarchical:    true,  // enable hierarchical by default
        gpuResourceName: "gpu",
    }
    
    // Parse configuration if provided
    if val, ok := config["timeHorizon"].(int64); ok {
        plugin.timeHorizon = val
    }
    if val, ok := config["alpha"].(float64); ok {
        plugin.alpha = val
    }
    if val, ok := config["hierarchical"].(bool); ok {
        plugin.hierarchical = val
    }
    if val, ok := config["gpuResourceName"].(string); ok {
        plugin.gpuResourceName = val
    }
    
    return plugin
}

// Name returns the plugin name
func (p *CAFGDScorePlugin) Name() string {
    return CAFGDPluginName
}

// Score calculates the CAFGD score for a given pod-node pair
// Higher score = better placement (minimizes fragmentation)
func (p *CAFGDScorePlugin) Score(
    pod *corev1.Pod,
    nodeName string,
    clusterState *simulator.ClusterState,
) (int64, error) {
    // Get the target node
    nodeInfo, err := clusterState.GetNodeInfo(nodeName)
    if err != nil {
        return 0, fmt.Errorf("node %s not found", nodeName)
    }

    // Get all nodes for cluster-wide fragmentation calculation
    allNodes := clusterState.GetAllNodes()

    // Calculate current cluster fragmentation (baseline)
    currentFrag := p.calculateClusterFragmentation(allNodes)

    // Simulate placing pod on this node
    simulatedNodes := p.simulatePlacement(allNodes, nodeInfo, pod)

    // Calculate fragmentation after placement
    newFrag := p.calculateClusterFragmentation(simulatedNodes)

    // Fragmentation gradient: increase in fragmentation
    gradient := newFrag - currentFrag

    // Lower gradient = better. Convert to score (higher is better)
    // Scale to 0-100 range
    maxGradient := p.estimateMaxGradient(allNodes, pod)
    if maxGradient > 0 {
        score := int64((1.0 - gradient/maxGradient) * 100)
        if score < 0 {
            score = 0
        }
        if score > 100 {
            score = 100
        }
        return score, nil
    }

    return 100, nil
}

// calculateClusterFragmentation computes the fragmentation rate of the entire cluster
// This implements the statistical fragmentation measure from the FGD/CAFGD paper
func (p *CAFGDScorePlugin) calculateClusterFragmentation(nodes []*simulator.NodeInfo) float64 {
    if len(nodes) == 0 {
        return 0.0
    }

    var totalFrag float64
    var totalGPUs int64

    for _, node := range nodes {
        // Get GPU allocatable
        gpuAllocatable := p.getGPUAllocatable(node)
        if gpuAllocatable == 0 {
            continue
        }

        // Calculate node-level fragmentation
        nodeFrag := p.calculateNodeFragmentation(node)
        totalFrag += nodeFrag * float64(gpuAllocatable)
        totalGPUs += gpuAllocatable
    }

    if totalGPUs == 0 {
        return 0.0
    }

    // Weighted average fragmentation across all GPUs
    return totalFrag / float64(totalGPUs)
}

// calculateNodeFragmentation computes fragmentation within a single node
// Considers hierarchical fragmentation if enabled
func (p *CAFGDScorePlugin) calculateNodeFragmentation(nodeInfo *simulator.NodeInfo) float64 {
    if !p.hierarchical {
        // Simple fragmentation: unused GPU capacity that is fragmented
        return p.calculateSimpleFragmentation(nodeInfo)
    }

    // Hierarchical fragmentation calculation
    // Level 1: Node-level fragmentation (how many GPUs are partially used)
    // Level 2: Per-GPU resource fragmentation
    
    gpuCapacity := p.getGPUAllocatable(nodeInfo)
    if gpuCapacity == 0 {
        return 0.0
    }

    // Count allocated GPUs and their resource usage
    var allocatedGPUs int64
    var totalGPUResources int64
    var usedGPUResources int64

    for _, pod := range nodeInfo.GetPods() {
        if gpuReq := p.getGPURequest(pod); gpuReq > 0 {
            allocatedGPUs += gpuReq
            totalGPUResources += gpuReq * 100 // 100 = 100% of a GPU
            usedGPUResources += gpuReq * p.estimateGPUUtilization(pod)
        }
    }

    // Level 1: Node fragmentation (fractional GPU allocation waste)
    nodeFrag := float64(allocatedGPUs%1)

    // Level 2: Per-GPU resource fragmentation
    var gpuFrag float64
    if totalGPUResources > 0 {
        gpuFrag = 1.0 - float64(usedGPUResources)/float64(totalGPUResources)
        nodeFrag = (nodeFrag + gpuFrag) / 2.0
    }

    return nodeFrag
}

// calculateSimpleFragmentation provides the basic FGD fragmentation measure
func (p *CAFGDScorePlugin) calculateSimpleFragmentation(nodeInfo *simulator.NodeInfo) float64 {
    gpuCap := p.getGPUAllocatable(nodeInfo)
    if gpuCap == 0 {
        return 0.0
    }

    // Count total GPU requests
    var totalGPURequests int64
    for _, pod := range nodeInfo.GetPods() {
        totalGPURequests += p.getGPURequest(pod)
    }

    // Fragmentation = unused capacity that cannot be allocated due to fragmentation
    if totalGPURequests > 0 {
        fractional := float64(totalGPURequests) - math.Floor(float64(totalGPURequests))
        return fractional / float64(gpuCap)
    }
    return 0.0
}

// simulatePlacement creates a copy of the cluster with the pod placed on the target node
func (p *CAFGDScorePlugin) simulatePlacement(
    nodes []*simulator.NodeInfo,
    targetNode *simulator.NodeInfo,
    pod *corev1.Pod,
) []*simulator.NodeInfo {
    // This should create a deep copy of the cluster state
    // and add the pod to the target node's pod list
    // Implementation depends on the actual ClusterState API
    return nodes
}

// estimateMaxGradient estimates the maximum possible fragmentation gradient
func (p *CAFGDScorePlugin) estimateMaxGradient(nodes []*simulator.NodeInfo, pod *corev1.Pod) float64 {
    // This is a heuristic - in practice, this would be calculated
    // by evaluating the worst-case placement scenario
    return 1.0
}

// getGPUAllocatable returns the number of allocatable GPUs on a node
func (p *CAFGDScorePlugin) getGPUAllocatable(nodeInfo *simulator.NodeInfo) int64 {
    // Implementation depends on the actual NodeInfo API
    // This would extract the GPU resource from the node's allocatable
    return 0
}

// getGPURequest extracts GPU request from a pod
func (p *CAFGDScorePlugin) getGPURequest(pod *corev1.Pod) int64 {
    // Implementation depends on the actual Pod API
    // This would sum GPU requests from all containers
    return 0
}

// estimateGPUUtilization estimates how much of a GPU a pod uses (0-100)
func (p *CAFGDScorePlugin) estimateGPUUtilization(pod *corev1.Pod) int64 {
    // In practice, this would come from profiling or historical data
    // For simulation, we use a default value
    return 100
}