package services

type StaticOriginDto struct {
	Upstream   string `json:"upstream" binding:"required"`
	HostHeader string `json:"hostHeader" binding:"required"`
	Port       int    `json:"port" binding:"min=1,max=65535"`
	Scheme     string `json:"scheme" binding:"required,oneof=Http Https"`
}

type S3OriginSpecDto struct {
	AwsSigsVersion int    `json:"awsSigsVersion" binding:"required,oneof=2 4"`
	S3AccessKeyId  string `json:"s3AccessKeyId" binding:"required"`
	S3SecretKey    string `json:"s3SecretKey" binding:"required"`
	S3BucketName   string `json:"s3BucketName" binding:"required"`
	S3Region       string `json:"s3Region" binding:"required"`
	S3Server       string `json:"s3Server" binding:"required"`
	S3ServerProto  string `json:"s3ServerProto" binding:"required,oneof=http https"`
	S3ServerPort   int    `json:"s3ServerPort" binding:"required"`
	S3Style        string `json:"s3Style" binding:"required,oneof=path virtual"`
}

type HostAliasDto struct {
	Name string `json:"name" binding:"required,hostname"`
}

type CacheKeyDto struct {
	Headers     []string `json:"headers,omitempty"`
	QueryParams []string `json:"queryParams,omitempty"`
}

type ServiceDto struct {
	Name         string           `json:"name" binding:"required,min=3,max=63"`
	OriginType   string           `json:"originType" binding:"required,oneof=s3 static"`
	StaticOrigin *StaticOriginDto `json:"staticOrigin,omitempty"`
	S3OriginSpec *S3OriginSpecDto `json:"s3OriginSpec,omitempty"`
	Cache        string           `json:"cache" binding:"required"`
	HostAliases  []HostAliasDto   `json:"hostAliases,omitempty"`
	CacheKey     *CacheKeyDto     `json:"cacheKey,omitempty"`

	SignedUrlsEnabled bool `json:"signedUrlsEnabled"`
	WafEnabled        bool `json:"wafEnabled"`
}

type CreateKeyDto struct {
	Name string `json:"name" binding:"required,min=3,max=32,alphanum"`
}

// Generic object to update several fields
type ServiceUpdateDto struct {
	Cache        string           `json:"cache,omitempty"`
	CacheKey     *CacheKeyDto     `json:"cacheKey,omitempty"`
	OriginType   string           `json:"originType,omitempty" binding:"omitempty,oneof=s3 static"`
	StaticOrigin *StaticOriginDto `json:"staticOrigin,omitempty"`
	S3OriginSpec *S3OriginSpecDto `json:"s3OriginSpec,omitempty"`
	WafEnabled   *bool            `json:"wafEnabled,omitempty"`
}
