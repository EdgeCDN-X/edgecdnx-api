package projects

// Todo revise this, allow longer project names
type ProjectDto struct {
	Name        string `json:"name" binding:"required,min=3,max=30"`
	Description string `json:"description" binding:"max=255"`
}
