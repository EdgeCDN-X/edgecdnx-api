package locations

import infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"

type CreateHealthCheckProfileDto struct {
	Name   string                                        `json:"name" binding:"required"`
	Probes []infrastructurev1alpha1.HealthCheckProbeSpec `json:"probes" binding:"required,min=1"`
}

type UpdateHealthCheckProfileDto struct {
	Probes *[]infrastructurev1alpha1.HealthCheckProbeSpec `json:"probes,omitempty"`
}
