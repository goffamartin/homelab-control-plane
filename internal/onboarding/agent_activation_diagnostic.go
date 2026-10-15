package onboarding

// Only fixed internal stage identifiers may be exported. Never wrap raw OS,
// YAML or subprocess errors: they can contain credential material.
type agentActivationCheckError struct{ check string }

func (e *agentActivationCheckError) Error() string {
	return "agent activation check failed: " + e.check
}
func (e *agentActivationCheckError) Unwrap() error { return ErrAgentActivationProfile }
