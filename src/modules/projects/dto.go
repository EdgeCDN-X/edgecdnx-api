package projects

// Todo revise this, allow longer project names
type ProjectDto struct {
	Name        string `json:"name" binding:"required,min=3,max=63"`
	Description string `json:"description" binding:"max=255"`
}

type RolePermission struct {
	Resource string `json:"resource" binding:"required"`
	Action   string `json:"action" binding:"required"`
}

type RoleDto struct {
	Name        string           `json:"name" binding:"required,max=63"`
	Permissions []RolePermission `json:"permissions" binding:"required,min=1,dive"`
}

type UpdateRoleDto struct {
	Permissions []RolePermission `json:"permissions" binding:"required,min=1,dive"`
}

type MemberDto struct {
	User string `json:"user" binding:"required,max=254"`
	Role string `json:"role" binding:"required,max=63"`
}

type MemberResponse struct {
	User string `json:"user"`
	Role string `json:"role"`
}

type RoleResponse struct {
	Name        string           `json:"name"`
	Permissions []RolePermission `json:"permissions"`
}
