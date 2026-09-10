package zones

type CreteZoneDto struct {
	Email string `json:"email" binding:"required,email"`
	Zone  string `json:"zone" binding:"required,fqdn"`
}

type UpdateZoneDto struct {
	Email string `json:"email,omitempty" binding:"omitempty,email"`
}

type CreateDNSEndpointDto struct {
	DNSName       string   `json:"dnsName" binding:"required,fqdn"`
	RoutingPolicy string   `json:"routingPolicy,omitempty" binding:"omitempty,eq=Simple"`
	RecordTTL     int      `json:"recordTTL" binding:"required,min=1"`
	RecordType    string   `json:"recordType" binding:"required,oneof=A AAAA CNAME TXT MX SRV NS"`
	Targets       []string `json:"targets" binding:"required,min=1,dive,required"`
}

type UpdateDNSEndpointDto struct {
	DNSName       string   `json:"dnsName,omitempty" binding:"omitempty,fqdn"`
	RoutingPolicy string   `json:"routingPolicy,omitempty" binding:"omitempty,eq=Simple"`
	RecordTTL     *int     `json:"recordTTL,omitempty" binding:"omitempty,min=1"`
	RecordType    string   `json:"recordType,omitempty" binding:"omitempty,oneof=A AAAA CNAME TXT MX SRV NS"`
	Targets       []string `json:"targets,omitempty" binding:"omitempty,min=1,dive,required"`
}
