package inithooks

// InitStatus is the JSON response for GET /_overcast/init.
type InitStatus struct {
	Completed map[Stage]bool `json:"completed"`
	Scripts   []ScriptResult `json:"scripts"`
}

// StageInfo is the JSON response for GET /_overcast/init/{stage}.
type StageInfo struct {
	Completed bool           `json:"completed"`
	Scripts   []ScriptResult `json:"scripts"`
}
