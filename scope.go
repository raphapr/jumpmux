package main

type scopeMode uint8

const (
	scopeAll scopeMode = iota
	scopeSession
)

func (scope scopeMode) toggle() scopeMode {
	if scope == scopeSession {
		return scopeAll
	}
	return scopeSession
}

func (scope scopeMode) label() string {
	if scope == scopeSession {
		return "session"
	}
	return "all"
}

func (scope scopeMode) displayLabel() string {
	if scope == scopeSession {
		return "Session"
	}
	return "All"
}

func scopeModeFromLabel(value string) scopeMode {
	if value == scopeSession.label() {
		return scopeSession
	}
	return scopeAll
}

func saveScopeMode(scope scopeMode) error {
	return saveConfigValue("default_scope", scope.label())
}
