package zones

type CreteZoneDto struct {
	Email string `json:"email" binding:"required,email"`
	Zone  string `json:"zone" binding:"required,fqdn"`
}

type UpdateZoneDto struct {
	Email string `json:"email,omitempty" binding:"omitempty,email"`
}
