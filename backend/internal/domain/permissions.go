package domain

// TenantPermissions are evaluated on every authenticated request. Nil is reserved
// for pre-1.5 tenants, whose existing effective access is retained until configured.
type TenantPermissions struct {
	AllNamespaces  bool     `json:"allNamespaces"`
	Namespaces     []string `json:"namespaces"`
	Submit         bool     `json:"submit"`
	TemplatesRead  bool     `json:"templatesRead"`
	TemplatesWrite bool     `json:"templatesWrite"`
	WorkflowsRead  bool     `json:"workflowsRead"`
	WorkflowsWrite bool     `json:"workflowsWrite"`
	Kill           bool     `json:"kill"`
	Delete         bool     `json:"delete"`
	AlertsRead     bool     `json:"alertsRead"`
	AlertsWrite    bool     `json:"alertsWrite"`
	ObjectStorage  bool     `json:"objectStorage"`
}

func DefaultTenant(role UserRole) string {
	if role == RoleAdmin {
		return "admin"
	}
	return "viewer"
}

func (u User) Can(capability string) bool {
	if u.Disabled {
		return false
	}
	p := u.Permissions
	if p == nil {
		switch capability {
		case "templatesRead", "workflowsRead", "alertsRead", "objectStorage":
			return true
		}
		return u.Role == RoleAdmin
	}
	switch capability {
	case "submit":
		return p.Submit
	case "templatesRead":
		return p.TemplatesRead
	case "templatesWrite":
		return p.TemplatesRead && p.TemplatesWrite
	case "workflowsRead":
		return p.WorkflowsRead
	case "workflowsWrite":
		return p.WorkflowsRead && p.WorkflowsWrite
	case "kill":
		return p.Kill
	case "delete":
		return p.Delete
	case "alertsRead":
		return p.AlertsRead
	case "alertsWrite":
		return p.AlertsRead && p.AlertsWrite
	case "objectStorage":
		return p.ObjectStorage
	}
	return false
}
