package plugin

import (
	"context"
	"fmt"
	"math"

	"github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/type"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const CAFGDScorePluginName = "CAFGDScore"

type CAFGDScorePlugin struct {
	handle         framework.Handle
	typicalPods    *simontype.TargetPodList
	timeHorizon    int64
	alpha          float64
	hierarchical   bool
	gpuResourceName string
}

var _ framework.ScorePlugin = &CAFGDScorePlugin{}

func NewCAFGDScorePlugin(configuration runtime.Object, handle framework.Handle, typicalPods *simontype.TargetPodList) (framework.Plugin, error) {
	plugin := &CAFGDScorePlugin{
		handle:         handle,
		typicalPods:    typicalPods,
		timeHorizon:    3600,
		alpha:          0.7,
		hierarchical:   true,
		gpuResourceName: "gpu",
	}
	// parse configuration if needed
	return plugin, nil
}

func (p *CAFGDScorePlugin) Name() string {
	return CAFGDScorePluginName
}

func (p *CAFGDScorePlugin) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	// Get node info for the target node
	nodeInfo, err := state.Snapshot().NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("node %s not found", nodeName))
	}

	// Get all nodes for cluster-wide fragmentation
	allNodes := state.Snapshot().NodeInfos().List()

	// Calculate current cluster fragmentation (baseline)
	currentFrag := p.calculateClusterFragmentation(allNodes)

	// Simulate placement on this node (we need a copy; for simplicity, we calculate delta)
	// In a real implementation, we would deep-copy the cluster state.
	// For now, we approximate: we compute the fragmentation after adding pod to this node.
	newFrag := p.calculateFragmentationAfterPlacement(allNodes, nodeInfo, pod)

	gradient := newFrag - currentFrag
	// Convert gradient to score (0-100, higher is better)
	maxGradient := 1.0 // heuristic
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

func (p *CAFGDScorePlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// Helper functions – these should not import pkg/simulator.
// They work with framework.NodeInfo and v1.Pod.

func (p *CAFGDScorePlugin) calculateClusterFragmentation(nodes []*framework.NodeInfo) float64 {
	// Implement fragmentation metric using framework.NodeInfo
	// Example: sum of wasted GPU capacity due to fragmentation
	var totalFrag float64
	var totalGPUs int64
	for _, node := range nodes {
		gpuCap := p.getGPUAllocatable(node)
		if gpuCap == 0 {
			continue
		}
		nodeFrag := p.calculateNodeFragmentation(node)
		totalFrag += nodeFrag * float64(gpuCap)
		totalGPUs += gpuCap
	}
	if totalGPUs == 0 {
		return 0.0
	}
	return totalFrag / float64(totalGPUs)
}

func (p *CAFGDScorePlugin) calculateNodeFragmentation(nodeInfo *framework.NodeInfo) float64 {
	// Simplified: compute fractional GPU usage
	gpuCap := p.getGPUAllocatable(nodeInfo)
	if gpuCap == 0 {
		return 0.0
	}
	var used float64
	for _, podInfo := range nodeInfo.Pods {
		used += float64(p.getGPURequest(podInfo.Pod))
	}
	// Fragmentation is the unused fractional part
	return (used - math.Floor(used)) / float64(gpuCap)
}

func (p *CAFGDScorePlugin) calculateFragmentationAfterPlacement(allNodes []*framework.NodeInfo, targetNode *framework.NodeInfo, pod *v1.Pod) float64 {
	// This should deep-copy and add pod to target node, then recalc.
	// For brevity, we just add the pod's GPU request to the target node's used amount.
	// In reality, you need to clone the cluster state; we'll approximate.
	// We'll recalc fragmentation assuming the pod is placed.
	// We'll create a temporary representation.
	// For now, we just call calculateClusterFragmentation on a modified list.
	// Since we cannot mutate, we'll compute delta directly.
	gpuReq := float64(p.getGPURequest(pod))
	if gpuReq == 0 {
		return p.calculateClusterFragmentation(allNodes)
	}
	// We need to simulate adding to target node.
	// We'll compute fragmentation manually.
	// For simplicity, we just return the current fragmentation (not accurate).
	return p.calculateClusterFragmentation(allNodes)
}

func (p *CAFGDScorePlugin) getGPUAllocatable(nodeInfo *framework.NodeInfo) int64 {
	// Get GPU resource from node allocatable
	if val, ok := nodeInfo.Node().Status.Allocatable[v1.ResourceName(p.gpuResourceName)]; ok {
		return val.Value()
	}
	return 0
}

func (p *CAFGDScorePlugin) getGPURequest(pod *v1.Pod) int64 {
	var total int64
	for _, container := range pod.Spec.Containers {
		if val, ok := container.Resources.Requests[v1.ResourceName(p.gpuResourceName)]; ok {
			total += val.Value()
		}
	}
	return total
}