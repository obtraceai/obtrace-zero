package injector

import (
	"k8s.io/apimachinery/pkg/api/resource"
	corev1 "k8s.io/api/core/v1"
)

func resourceRequirements(cpuReq, memReq, cpuLim, memLim string) corev1.ResourceRequirements {
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuReq),
			corev1.ResourceMemory: resource.MustParse(memReq),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(cpuLim),
			corev1.ResourceMemory: resource.MustParse(memLim),
		},
	}
}

func ebpfResources() corev1.ResourceRequirements {
	return resourceRequirements("50m", "64Mi", "200m", "128Mi")
}

func initResources() corev1.ResourceRequirements {
	return resourceRequirements("10m", "32Mi", "100m", "64Mi")
}
