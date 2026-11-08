package authz

// Trust-pool (认证池) is the first independently grantable operator area
// (P5-C). It is deliberately absent from the admin baseline: managing the pool
// is an operational duty that must be delegated per user, and a delegated
// operator must not gain the other RootAuth marketplace endpoints. Root remains
// a superuser and therefore never needs an explicit policy row.
const (
	ResourceTrustPool = "trust_pool"

	ActionList    = "list"
	ActionCreate  = "create"
	ActionUpdate  = "update"
	ActionDelete  = "delete"
	ActionApprove = "approve"
)

var (
	TrustPoolRead    = Permission{Resource: ResourceTrustPool, Action: ActionRead}
	TrustPoolList    = Permission{Resource: ResourceTrustPool, Action: ActionList}
	TrustPoolCreate  = Permission{Resource: ResourceTrustPool, Action: ActionCreate}
	TrustPoolUpdate  = Permission{Resource: ResourceTrustPool, Action: ActionUpdate}
	TrustPoolDelete  = Permission{Resource: ResourceTrustPool, Action: ActionDelete}
	TrustPoolApprove = Permission{Resource: ResourceTrustPool, Action: ActionApprove}
)

func init() {
	RegisterResource(ResourceDefinition{
		Resource: ResourceTrustPool,
		LabelKey: "Trust Pool",
		Actions: []ActionDefinition{
			{
				Action:         ActionList,
				LabelKey:       "List trust pool entries",
				DescriptionKey: "View the trust-pool list, filters, and the pending two-person approval queue.",
			},
			{
				Action:         ActionRead,
				LabelKey:       "Read trust pool entry",
				DescriptionKey: "View one trust-pool entry and its approval history.",
			},
			{
				Action:         ActionCreate,
				LabelKey:       "Create trust pool entries",
				DescriptionKey: "Submit a new host address for the trust pool; it stays pending until a second operator approves it.",
			},
			{
				Action:         ActionUpdate,
				LabelKey:       "Update trust pool entries",
				DescriptionKey: "Submit changed trust, endpoint, or policy fields for an existing trust-pool entry.",
			},
			{
				Action:         ActionDelete,
				LabelKey:       "Delete trust pool entries",
				DescriptionKey: "Submit a removal of a non-seed trust-pool entry.",
			},
			{
				Action:         ActionApprove,
				LabelKey:       "Review trust pool changes",
				DescriptionKey: "Approve or reject another operator's pending trust-pool change.",
			},
		},
	})
}
