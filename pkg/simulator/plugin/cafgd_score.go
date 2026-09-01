package plugin

import (
	"context"
	"fmt"
	"math"

	simontype "github.com/hkust-adsl/kubernetes-scheduler-simulator/pkg/type"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/kubernetes/pkg/scheduler/framework"
)

const CAFGDScorePluginName = "CAFGDScore"

// CAFGDScorePlugin implements the Cluster-Aware FGD algorithm.
type CAFGDScorePlugin struct {
	handle         framework.Handle
	typicalPods    *simontype.TargetPodList
	timeHorizon    int64
	alpha          float64
	hierarchical   bool
	gpuResourceName string
}

var _ framework.ScorePlugin = &CAFGDScorePlugin{}

// NewCAFGDScorePlugin creates a new CAFGD plugin instance.
func NewCAFGDScorePlugin(configuration runtime.Object, handle framework.Handle, typicalPods *simontype.TargetPodList) (framework.Plugin, error) {
	plugin := &CAFGDScorePlugin{
		handle:         handle,
		typicalPods:    typicalPods,
		timeHorizon:    3600,
		alpha:          0.7,
		hierarchical:   true,
		gpuResourceName: "gpu",
	}
	// Optionally parse configuration from 'configuration' object.
	return plugin, nil
}

// Name returns the plugin name.
func (p *CAFGDScorePlugin) Name() string {
	return CAFGDScorePluginName
}

// Score computes a score for placing the pod on the given node.
// Higher score means better placement (lower fragmentation gradient).
func (p *CAFGDScorePlugin) Score(ctx context.Context, state *framework.CycleState, pod *v1.Pod, nodeName string) (int64, *framework.Status) {
	// Get the snapshot of the cluster.
	snapshot := p.handle.Snapshot()
	if snapshot == nil {
		return 0, framework.NewStatus(framework.Error, "snapshot is nil")
	}

	// Get node info for the target node.
	targetNodeInfo, err := snapshot.NodeInfos().Get(nodeName)
	if err != nil {
		return 0, framework.NewStatus(framework.Error, fmt.Sprintf("node %s not found", nodeName))
	}

	// All nodes for cluster-wide fragmentation.
	allNodes := snapshot.NodeInfos().List()

	// Current fragmentation (baseline).
	currentFrag := p.calculateClusterFragmentation(allNodes, "", nil)

	// Fragmentation if we add the pod to the target node.
	newFrag := p.calculateClusterFragmentation(allNodes, nodeName, pod)

	// Fragmentation gradient = increase in fragmentation.
	gradient := newFrag - currentFrag
	// Convert gradient to score (0-100, higher is better).
	// We use a simple heuristic: score = (1 - gradient) * 100, clipped.
	score := int64((1.0 - gradient) * 100)
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score, framework.NewStatus(framework.Success, "")
}

// ScoreExtensions returns nil (no normalization needed).
func (p *CAFGDScorePlugin) ScoreExtensions() framework.ScoreExtensions {
	return nil
}

// calculateClusterFragmentation computes the total fragmentation across all nodes.
// If addNode is non-empty, the pod is considered added to that node.
func (p *CAFGDScorePlugin) calculateClusterFragmentation(nodes []*framework.NodeInfo, addNode string, pod *v1.Pod) float64 {
	if len(nodes) == 0 {
		return 0.0
	}
	var totalFrag float64
	var totalGPUs int64

	for _, node := range nodes {
		gpuCap := p.getGPUAllocatable(node)
		if gpuCap == 0 {
			continue
		}
		var addPod *v1.Pod
		if node.Node().Name == addNode {
			addPod = pod
		}
		nodeFrag := p.calculateNodeFragmentation(node, addPod)
		totalFrag += nodeFrag * float64(gpuCap)
		totalGPUs += gpuCap
	}
	if totalGPUs == 0 {
		return 0.0
	}
	return totalFrag / float64(totalGPUs)
}

// calculateNodeFragmentation computes fragmentation for a single node,
// optionally including an extra pod.
func (p *CAFGDScorePlugin) calculateNodeFragmentation(nodeInfo *framework.NodeInfo, addPod *v1.Pod) float64 {
	gpuCap := p.getGPUAllocatable(nodeInfo)
	if gpuCap == 0 {
		return 0.0
	}
	// Sum GPU requests from existing pods.
	var used float64
	for _, podInfo := range nodeInfo.Pods {
		used += float64(p.getGPURequest(podInfo.Pod))
	}
	// Add the extra pod if specified.
	if addPod != nil {
		used += float64(p.getGPURequest(addPod))
	}
	// Fragmentation: fractional part of used GPUs divided by capacity.
	fractional := used - math.Floor(used)
	return fractional / float64(gpuCap)
}

// getGPUAllocatable returns the GPU capacity for a node.
func (p *CAFGDScorePlugin) getGPUAllocatable(nodeInfo *framework.NodeInfo) int64 {
	if val, ok := nodeInfo.Node().Status.Allocatable[v1.ResourceName(p.gpuResourceName)]; ok {
		return val.Value()
	}
	return 0
}

// getGPURequest extracts the total GPU request from a pod.
func (p *CAFGDScorePlugin) getGPURequest(pod *v1.Pod) int64 {
	if pod == nil {
		return 0
	}
	var total int64
	for _, container := range pod.Spec.Containers {
		if val, ok := container.Resources.Requests[v1.ResourceName(p.gpuResourceName)]; ok {
			total += val.Value()
		}
	}
	return total
}