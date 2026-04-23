package authz

// permissions maps each role to the set of actions it may perform.
// Matches spec Section 5.2 role matrix exactly.
var permissions = map[string]map[Action]bool{
	"admin": {
		ViewCatalog:      true,
		SearchTool:       true,
		ViewStatus:       true,
		CallTool:         true,
		RefreshDiscovery: true,
		EnableTool:       true,
		DisableTool:      true,
		RegisterServer:   true,
		UpdateServer:     true,
		DeleteServer:     true,
		BindSecret:       true,
	},
	"operator": {
		ViewCatalog:      true,
		SearchTool:       true,
		ViewStatus:       true,
		RefreshDiscovery: true,
		EnableTool:       true,
		DisableTool:      true,
	},
	"viewer": {
		ViewCatalog: true,
		SearchTool:  true,
		ViewStatus:  true,
	},
	"agent": {
		ViewCatalog: true,
		SearchTool:  true,
		CallTool:    true,
	},
}

// roleAllows returns true if the given role permits the action.
func roleAllows(role string, action Action) bool {
	perms, ok := permissions[role]
	if !ok {
		return false
	}
	return perms[action]
}
