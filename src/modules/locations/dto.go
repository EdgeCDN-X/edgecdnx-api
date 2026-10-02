package locations

import infrastructurev1alpha1 "github.com/EdgeCDN-X/edgecdnx-controller/api/v1alpha1"

type CreateLocationDto struct {
	Name              string                                 `json:"name" binding:"required"`
	NodeGroups        []infrastructurev1alpha1.NodeGroupSpec `json:"nodeGroups,omitempty"`
	GeoLookup         infrastructurev1alpha1.GeoLookupSpec   `json:"geoLookup,omitempty"`
	Weight            int32                                  `json:"weight,omitempty"`
	FallbackLocations []string                               `json:"fallbackLocations,omitempty"`
}

type UpdateLocationDto struct {
	NodeGroups        *[]infrastructurev1alpha1.NodeGroupSpec `json:"nodeGroups,omitempty"`
	GeoLookup         *infrastructurev1alpha1.GeoLookupSpec   `json:"geoLookup,omitempty"`
	Weight            *int32                                  `json:"weight,omitempty"`
	FallbackLocations *[]string                               `json:"fallbackLocations,omitempty"`
}
