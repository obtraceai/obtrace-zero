package discovery

import (
	"context"
	"fmt"
	"time"

	"github.com/obtraceai/obtrace-zero/pkg/crd"
	"github.com/obtraceai/obtrace-zero/pkg/detector"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type WorkloadInfo struct {
	Name       string
	Namespace  string
	Kind       string
	Replicas   int32
	Language   crd.Language
	Framework  string
	Strategy   crd.InstrumentationStrategy
	Confidence float64
	PodSpec    corev1.PodSpec
}

type DiscoveryService struct {
	client   client.Client
	detector *detector.Detector
}

func New(c client.Client) *DiscoveryService {
	return &DiscoveryService{
		client:   c,
		detector: detector.New(),
	}
}

func (ds *DiscoveryService) DiscoverAll(ctx context.Context, namespaces []string) ([]WorkloadInfo, error) {
	var allWorkloads []WorkloadInfo

	if len(namespaces) == 0 {
		namespaces = []string{""}
	}

	for _, ns := range namespaces {
		deployments, err := ds.discoverDeployments(ctx, ns)
		if err != nil {
			return nil, fmt.Errorf("discovering deployments in %s: %w", ns, err)
		}
		allWorkloads = append(allWorkloads, deployments...)

		statefulSets, err := ds.discoverStatefulSets(ctx, ns)
		if err != nil {
			return nil, fmt.Errorf("discovering statefulsets in %s: %w", ns, err)
		}
		allWorkloads = append(allWorkloads, statefulSets...)

		daemonSets, err := ds.discoverDaemonSets(ctx, ns)
		if err != nil {
			return nil, fmt.Errorf("discovering daemonsets in %s: %w", ns, err)
		}
		allWorkloads = append(allWorkloads, daemonSets...)
	}

	return allWorkloads, nil
}

func (ds *DiscoveryService) discoverDeployments(ctx context.Context, namespace string) ([]WorkloadInfo, error) {
	var list appsv1.DeploymentList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := ds.client.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	var results []WorkloadInfo
	for _, dep := range list.Items {
		if ds.isSystemWorkload(dep.Namespace, dep.Name) {
			continue
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      dep.Spec.Template.Labels,
				Annotations: dep.Spec.Template.Annotations,
			},
			Spec: dep.Spec.Template.Spec,
		}
		det := ds.detector.Detect(ctx, pod)
		replicas := int32(1)
		if dep.Spec.Replicas != nil {
			replicas = *dep.Spec.Replicas
		}
		results = append(results, WorkloadInfo{
			Name:       dep.Name,
			Namespace:  dep.Namespace,
			Kind:       "Deployment",
			Replicas:   replicas,
			Language:   det.Language,
			Framework:  det.Framework,
			Strategy:   det.Strategy,
			Confidence: det.Confidence,
			PodSpec:    dep.Spec.Template.Spec,
		})
	}
	return results, nil
}

func (ds *DiscoveryService) discoverStatefulSets(ctx context.Context, namespace string) ([]WorkloadInfo, error) {
	var list appsv1.StatefulSetList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := ds.client.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	var results []WorkloadInfo
	for _, sts := range list.Items {
		if ds.isSystemWorkload(sts.Namespace, sts.Name) {
			continue
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      sts.Spec.Template.Labels,
				Annotations: sts.Spec.Template.Annotations,
			},
			Spec: sts.Spec.Template.Spec,
		}
		det := ds.detector.Detect(ctx, pod)
		replicas := int32(1)
		if sts.Spec.Replicas != nil {
			replicas = *sts.Spec.Replicas
		}
		results = append(results, WorkloadInfo{
			Name:       sts.Name,
			Namespace:  sts.Namespace,
			Kind:       "StatefulSet",
			Replicas:   replicas,
			Language:   det.Language,
			Framework:  det.Framework,
			Strategy:   det.Strategy,
			Confidence: det.Confidence,
			PodSpec:    sts.Spec.Template.Spec,
		})
	}
	return results, nil
}

func (ds *DiscoveryService) discoverDaemonSets(ctx context.Context, namespace string) ([]WorkloadInfo, error) {
	var list appsv1.DaemonSetList
	opts := []client.ListOption{}
	if namespace != "" {
		opts = append(opts, client.InNamespace(namespace))
	}
	if err := ds.client.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	var results []WorkloadInfo
	for _, ds_ := range list.Items {
		if ds.isSystemWorkload(ds_.Namespace, ds_.Name) {
			continue
		}
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      ds_.Spec.Template.Labels,
				Annotations: ds_.Spec.Template.Annotations,
			},
			Spec: ds_.Spec.Template.Spec,
		}
		det := ds.detector.Detect(ctx, pod)
		results = append(results, WorkloadInfo{
			Name:       ds_.Name,
			Namespace:  ds_.Namespace,
			Kind:       "DaemonSet",
			Replicas:   0,
			Language:   det.Language,
			Framework:  det.Framework,
			Strategy:   det.Strategy,
			Confidence: det.Confidence,
			PodSpec:    ds_.Spec.Template.Spec,
		})
	}
	return results, nil
}

func (ds *DiscoveryService) isSystemWorkload(namespace, name string) bool {
	systemNamespaces := map[string]bool{
		"kube-system":     true,
		"kube-public":     true,
		"kube-node-lease": true,
		"cert-manager":    true,
		"linkerd":         true,
		"linkerd-viz":     true,
		"argocd":          true,
		"obtrace-infra":   true,
	}
	return systemNamespaces[namespace]
}

func (ds *DiscoveryService) ToDiscoveredWorkloads(workloads []WorkloadInfo) []crd.DiscoveredWorkload {
	result := make([]crd.DiscoveredWorkload, len(workloads))
	for i, w := range workloads {
		result[i] = crd.DiscoveredWorkload{
			Name:           w.Name,
			Namespace:      w.Namespace,
			Kind:           w.Kind,
			Language:       w.Language,
			Framework:      w.Framework,
			Strategy:       w.Strategy,
			Instrumented:   false,
			LastDetectedAt: metav1.Time{Time: time.Now()},
		}
	}
	return result
}
