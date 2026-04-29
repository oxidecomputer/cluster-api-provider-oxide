package v1alpha1

// OxideMachine constants. Note that most OxideMachine reasons are built dynamically
// from the Oxide instance's RunState, so we don't enumerate them here.
const (
	ReasonInstanceDeleted = "InstanceDeleted"
	ReasonInstanceUnknown = "InstanceUnknown"
)

// OxideCluster constants.
const FloatingIPAttachedCondition = "FloatingIPAttached"

const (
	// Reasons for floating IP provisioning.
	ReasonFloatingIPProvisioned = "FloatingIPProvisioned"

	// Reasons for floating IP attachment.
	ReasonFloatingIPAttached            = "FloatingIPAttached"
	ReasonWaitingForControlPlaneMachine = "WaitingForControlPlaneMachine"
)
