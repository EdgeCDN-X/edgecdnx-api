package apikeys

import (
	"bytes"
	"encoding/json"
	"time"
)

// CreateAPIKeyDto creates a key. Omitting expiresAt (or sending null) creates a key that never expires.
type CreateAPIKeyDto struct {
	DisplayName string     `json:"displayName" binding:"required,max=128"`
	ExpiresAt   *time.Time `json:"expiresAt"`
	Roles       []string   `json:"roles"`
}

// UpdateAPIKeyDto fields are optional; omitted fields remain unchanged.
// Sending "roles": [] removes all role assignments.
// Sending "expiresAt": null removes the expiry, so the key never expires.
type UpdateAPIKeyDto struct {
	DisplayName *string      `json:"displayName" binding:"omitempty,min=1,max=128"`
	ExpiresAt   OptionalTime `json:"expiresAt"`
	Roles       *[]string    `json:"roles"`
}

// OptionalTime distinguishes an omitted JSON field (Set=false) from an explicit null (Set=true, Value=nil).
type OptionalTime struct {
	Set   bool
	Value *time.Time
}

func (o *OptionalTime) UnmarshalJSON(data []byte) error {
	o.Set = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		o.Value = nil
		return nil
	}
	var t time.Time
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	o.Value = &t
	return nil
}

type APIKeyDto struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	DisplayName string   `json:"displayName"`
	Project     string   `json:"project"`
	Roles       []string `json:"roles"`
	// ExpiresAt is null when the key never expires.
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

type CreatedAPIKeyDto struct {
	APIKeyDto
	// Token is returned only once, on creation.
	Token string `json:"token"`
}
