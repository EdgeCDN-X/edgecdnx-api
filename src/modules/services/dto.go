package services

type StaticOriginDto struct {
	Upstream   string `json:"upstream" binding:"required"`
	HostHeader string `json:"hostHeader"`
	Port       int    `json:"port" binding:"min=1,max=65535"`
	Scheme     string `json:"scheme" binding:"required,oneof=http https"`
}

type ServiceDto struct {
	Name         string           `json:"name" binding:"required,min=3,max=63"`
	OriginType   string           `json:"originType" binding:"required,oneof=s3 static"`
	StaticOrigin *StaticOriginDto `json:"staticOrigin,omitempty"`
}
