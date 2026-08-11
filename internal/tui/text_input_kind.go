package tui

// textInputCategory distinguishes what an active text input is editing. It
// replaces a raw string that used to be reinterpreted three incompatible
// ways across this package: exact identity ("param", "profile"), string
// prefix ("service-ports:"), and casts to two unrelated enum types
// (advancedOptionKind, containerResourceKind). A new editable field only
// needs a new category + accessor here instead of touching every call site
// that used to guess the encoding.
type textInputCategory int

const (
	textInputNone textInputCategory = iota
	textInputProfile
	textInputParam
	textInputAdvancedOption
	textInputContainerResource
	textInputServicePort
)

// textInputKind identifies what Model.textInputValue is currently editing.
// Only the field matching category is meaningful; the others are zero.
type textInputKind struct {
	category  textInputCategory
	advanced  advancedOptionKind
	resource  containerResourceKind
	serviceID string
}

func newProfileTextInput() textInputKind {
	return textInputKind{category: textInputProfile}
}

func newParamTextInput() textInputKind {
	return textInputKind{category: textInputParam}
}

func newAdvancedTextInput(kind advancedOptionKind) textInputKind {
	return textInputKind{category: textInputAdvancedOption, advanced: kind}
}

func newContainerResourceTextInput(kind containerResourceKind) textInputKind {
	return textInputKind{category: textInputContainerResource, resource: kind}
}

func newServicePortTextInput(serviceID string) textInputKind {
	return textInputKind{category: textInputServicePort, serviceID: serviceID}
}

func (k textInputKind) isProfile() bool           { return k.category == textInputProfile }
func (k textInputKind) isParam() bool             { return k.category == textInputParam }
func (k textInputKind) isAdvanced() bool          { return k.category == textInputAdvancedOption }
func (k textInputKind) isContainerResource() bool { return k.category == textInputContainerResource }
func (k textInputKind) isServicePort() bool       { return k.category == textInputServicePort }

func (k textInputKind) advancedKind() (advancedOptionKind, bool) {
	if k.category != textInputAdvancedOption {
		return "", false
	}
	return k.advanced, true
}

func (k textInputKind) resourceKind() (containerResourceKind, bool) {
	if k.category != textInputContainerResource {
		return "", false
	}
	return k.resource, true
}

// isResource reports whether this input is editing the given container
// resource kind specifically (e.g. the runtime/context pickers need to know
// which one they are, not just that it's some container resource).
func (k textInputKind) isResource(kind containerResourceKind) bool {
	resource, ok := k.resourceKind()
	return ok && resource == kind
}

func (k textInputKind) serviceIDValue() (string, bool) {
	if k.category != textInputServicePort {
		return "", false
	}
	return k.serviceID, true
}
