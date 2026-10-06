package zones

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

type CreteZoneDto struct {
	Email string `json:"email" binding:"required,email"`
	Zone  string `json:"zone" binding:"required,fqdn"`
}

type UpdateZoneDto struct {
	Email string `json:"email,omitempty" binding:"omitempty,email"`
}

type CreateDNSEndpointDto struct {
	DNSName       string                `json:"dnsName" binding:"required"`
	RoutingPolicy string                `json:"routingPolicy,omitempty" binding:"omitempty,oneof=Simple Weighted Failover Geolocation RoundRobin"`
	RecordTTL     int                   `json:"recordTTL" binding:"required,min=1"`
	RecordType    string                `json:"recordType" binding:"required,oneof=A AAAA CNAME TXT MX SRV NS"`
	Targets       []string              `json:"targets" binding:"omitempty,dive,required"`
	RouteSelector *metav1.LabelSelector `json:"routeSelector,omitempty"`
}

type UpdateDNSEndpointDto struct {
	DNSName       string                `json:"dnsName,omitempty" binding:"omitempty"`
	RoutingPolicy string                `json:"routingPolicy,omitempty" binding:"omitempty,oneof=Simple Weighted Failover Geolocation RoundRobin"`
	RecordTTL     *int                  `json:"recordTTL,omitempty" binding:"omitempty,min=1"`
	RecordType    string                `json:"recordType,omitempty" binding:"omitempty,oneof=A AAAA CNAME TXT MX SRV NS"`
	Targets       []string              `json:"targets,omitempty" binding:"omitempty,dive,required"`
	RouteSelector *metav1.LabelSelector `json:"routeSelector,omitempty"`
}
